package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"
)

func capture(t *testing.T, options HandlerOptions, emit func(*slog.Logger)) map[string]any {
	t.Helper()
	var output bytes.Buffer
	handler, err := NewSanitizingHandler(slog.NewJSONHandler(&output, nil), options)
	if err != nil {
		t.Fatal(err)
	}
	emit(slog.New(handler))
	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatalf("decode: %v; output: %s", err, output.String())
	}
	delete(record, "time")
	delete(record, "level")
	delete(record, "msg")
	return record
}

type forbiddenValue struct{}

func (forbiddenValue) MarshalJSON() ([]byte, error) { panic("MarshalJSON must not run") }
func (forbiddenValue) String() string               { panic("String must not run") }

type forbiddenScalar string

func (forbiddenScalar) MarshalJSON() ([]byte, error) { panic("scalar MarshalJSON must not run") }

type brokenError struct{}

func (brokenError) Error() string { panic("error internals") }

type forbiddenValuer struct{}

func (forbiddenValuer) LogValue() slog.Value { panic("sensitive LogValuer must not run") }

func TestGroupPathsUseTheSameRedactionAndDepthRules(t *testing.T) {
	for _, emit := range []func(*slog.Logger){
		func(logger *slog.Logger) { logger.WithGroup("access_token").Info("done", "value", forbiddenValuer{}) },
		func(logger *slog.Logger) {
			logger.Info("done", slog.Group("access_token", slog.Any("value", forbiddenValuer{})))
		},
	} {
		record := capture(t, HandlerOptions{}, emit)
		if !reflect.DeepEqual(record, map[string]any{"access_token": "[REDACTED]"}) {
			t.Fatalf("record = %#v", record)
		}
	}
	for _, emit := range []func(*slog.Logger){
		func(logger *slog.Logger) {
			logger.WithGroup("a").WithGroup("b").WithGroup("c").Info("done", "value", 1)
		},
		func(logger *slog.Logger) {
			logger.Info("done", slog.Group("a", slog.Group("b", slog.Group("c", "value", 1))))
		},
	} {
		record := capture(t, HandlerOptions{MaxDepth: 1}, emit)
		if !reflect.DeepEqual(record, map[string]any{"a": map[string]any{"b": "[TRUNCATED]"}}) {
			t.Fatalf("record = %#v", record)
		}
	}
}

func TestSharedGroupBudgetIncludesStaticContextAndRecordFields(t *testing.T) {
	record := capture(t, HandlerOptions{
		MaxAttributes: 4,
		ContextAttrs:  func(context.Context) []slog.Attr { return []slog.Attr{slog.String("request_id", "request-1")} },
	}, func(logger *slog.Logger) {
		logger.WithGroup("request").With("service", "api").Info("done", slog.Group("", "status", 200), "omitted", 1)
	})
	want := map[string]any{"request": map[string]any{"service": "api", "request_id": "request-1", "status": float64(200)}}
	if !reflect.DeepEqual(record, want) {
		t.Fatalf("record = %#v", record)
	}
}

func TestBoundedContainersAndUnsupportedObjects(t *testing.T) {
	recursive := map[string]any{}
	recursive["self"] = recursive
	metadata := map[string]string{"Authorization": "hidden", "name": "visible"}
	record := capture(t, HandlerOptions{MaxDepth: 3}, func(logger *slog.Logger) {
		logger.Info("done",
			"object", forbiddenValue{}, "pointer", &forbiddenValue{},
			"scalar", forbiddenScalar("visible"), "error", brokenError{},
			"metadata", metadata, "array", [2]int{1, 2}, "slice", []string{"a", "b"},
			"bytes", []byte("hidden"), "recursive", recursive,
			"valuer", groupValuer{}, "duration", time.Second, "number", int64(9_007_199_254_740_993),
		)
	})
	for _, key := range []string{"object", "pointer", "error"} {
		if record[key] != "[UNSERIALIZABLE]" {
			t.Fatalf("%s = %#v", key, record[key])
		}
	}
	if record["scalar"] != "visible" || record["bytes"] != "<bytes:6>" || record["duration"] != float64(time.Second) {
		t.Fatalf("record = %#v", record)
	}
	if !reflect.DeepEqual(record["metadata"], map[string]any{"Authorization": "[REDACTED]", "name": "visible"}) {
		t.Fatalf("metadata = %#v", record["metadata"])
	}
	if !reflect.DeepEqual(record["array"], []any{float64(1), float64(2)}) || !reflect.DeepEqual(record["slice"], []any{"a", "b"}) {
		t.Fatalf("containers = %#v", record)
	}
	encoded, _ := json.Marshal(record["recursive"])
	if !strings.Contains(string(encoded), "[TRUNCATED]") {
		t.Fatalf("recursive = %s", encoded)
	}
	if metadata["Authorization"] != "hidden" {
		t.Fatal("caller map was changed")
	}
}

func TestWithAttrsAndGroupsAreIsolated(t *testing.T) {
	var output bytes.Buffer
	handler, err := NewSanitizingHandler(slog.NewJSONHandler(&output, nil), HandlerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	attrs := []slog.Attr{slog.String("name", "original")}
	base := handler.WithAttrs(attrs)
	attrs[0] = slog.String("name", "changed")
	slog.New(base.WithGroup("left")).Info("done", "value", 1)
	slog.New(base.WithGroup("right")).Info("done", "value", 2)
	slog.New(base).Info("done", "value", 3)
	decoder := json.NewDecoder(&output)
	for _, group := range []string{"left", "right", ""} {
		var record map[string]any
		if err := decoder.Decode(&record); err != nil {
			t.Fatal(err)
		}
		if record["name"] != "original" {
			t.Fatalf("record = %#v", record)
		}
		if group == "" && (record["left"] != nil || record["right"] != nil) {
			t.Fatalf("inherited groups = %#v", record)
		}
		if group != "" && record[group] == nil {
			t.Fatalf("missing group %s: %#v", group, record)
		}
	}
}

type enabledPanicHandler struct{ failureHandler }

func (*enabledPanicHandler) Enabled(context.Context, slog.Level) bool { panic("enabled panic") }

func TestProjectExtensionPanicsPropagate(t *testing.T) {
	for name, next := range map[string]slog.Handler{
		"enabled": &enabledPanicHandler{},
		"handle":  &failureHandler{enabled: true, panics: true},
		"context": slog.NewJSONHandler(&bytes.Buffer{}, nil),
	} {
		t.Run(name, func(t *testing.T) {
			options := HandlerOptions{}
			if name == "context" {
				options.ContextAttrs = func(context.Context) []slog.Attr { panic("context panic") }
			}
			handler, err := NewSanitizingHandler(next, options)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if recover() == nil {
					t.Fatal("project panic was swallowed")
				}
			}()
			slog.New(handler).Info("done")
		})
	}
}
