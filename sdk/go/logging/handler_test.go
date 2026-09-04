package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"testing"
	"time"
)

type typedNilHandler struct{}

func (*typedNilHandler) Enabled(context.Context, slog.Level) bool  { return true }
func (*typedNilHandler) Handle(context.Context, slog.Record) error { return nil }
func (*typedNilHandler) WithAttrs([]slog.Attr) slog.Handler        { return (*typedNilHandler)(nil) }
func (*typedNilHandler) WithGroup(string) slog.Handler             { return (*typedNilHandler)(nil) }

type failureHandler struct {
	err     error
	panics  bool
	enabled bool
	handled int
}

func (handler *failureHandler) Enabled(context.Context, slog.Level) bool { return handler.enabled }
func (handler *failureHandler) Handle(context.Context, slog.Record) error {
	handler.handled++
	if handler.panics {
		panic("writer secret must not escape")
	}
	return handler.err
}
func (handler *failureHandler) WithAttrs([]slog.Attr) slog.Handler { return handler }
func (handler *failureHandler) WithGroup(string) slog.Handler      { return handler }

type unsafeError struct{}

func (unsafeError) Error() string { return "database password=hidden" }

type recursiveValue struct {
	Child *recursiveValue `json:"child"`
}

type groupValuer struct{}

func (groupValuer) LogValue() slog.Value {
	return slog.GroupValue(slog.String("client-secret", "hidden"), slog.String("name", "visible"))
}

func TestSanitizingHandlerProducesBoundedSafeJSON(t *testing.T) {
	var output bytes.Buffer
	base := slog.NewJSONHandler(&output, nil)
	handler, err := NewSanitizingHandler(base, HandlerOptions{
		MaxMessageBytes:    32,
		MaxStringBytes:     32,
		MaxAttributes:      16,
		MaxDepth:           2,
		ExtraSensitiveKeys: []string{"tenant credential"},
		ContextAttrs: func(context.Context) []slog.Attr {
			return []slog.Attr{slog.String("request_id", "request-1")}
		},
	})
	if err != nil {
		t.Fatalf("NewSanitizingHandler() error = %v", err)
	}

	metadata := map[string]any{
		"apiKey": "secret-api-key",
		"nested": map[string]any{
			"Authorization": "Bearer secret",
			"depth":         map[string]any{"value": "hidden by depth"},
		},
		"error": unsafeError{},
		"ratio": math.Inf(1),
	}
	logger := slog.New(handler).With("tenant_credential", "secret-credential").WithGroup("request")
	logger.Info("abcdefghijklmnopqrstuvwxyz0123456789", "metadata", metadata, "long", "abcdefghijklmnopqrstuvwxyz0123456789")

	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatalf("日志不是合法单行 JSON: %v\n%s", err, output.String())
	}
	if bytes.Count(output.Bytes(), []byte{'\n'}) != 1 {
		t.Fatalf("一条日志必须只占一个物理行: %q", output.String())
	}
	if got := record["msg"]; got != "abcdefghijklmnopqrstu[TRUNCATED]" {
		t.Fatalf("msg = %#v", got)
	}
	if got := record["tenant_credential"]; got != redactedValue {
		t.Fatalf("tenant_credential = %#v", got)
	}
	request, ok := record["request"].(map[string]any)
	if !ok {
		t.Fatalf("request = %#v", record["request"])
	}
	if request["request_id"] != "request-1" {
		t.Fatalf("request_id = %#v", request["request_id"])
	}
	cleanMetadata := request["metadata"].(map[string]any)
	if cleanMetadata["apiKey"] != redactedValue {
		t.Fatalf("apiKey = %#v", cleanMetadata["apiKey"])
	}
	if cleanMetadata["ratio"] != unserializableValue {
		t.Fatalf("ratio = %#v", cleanMetadata["ratio"])
	}
	if metadata["apiKey"] != "secret-api-key" {
		t.Fatal("调用方 metadata 被修改")
	}
}

func TestSanitizingHandlerPreservesLargeIntegersAndResolvesGroups(t *testing.T) {
	var output bytes.Buffer
	handler, err := NewSanitizingHandler(slog.NewJSONHandler(&output, nil), HandlerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(handler.WithAttrs([]slog.Attr{slog.Int64("value", 9_007_199_254_740_993)})).WithGroup("outer")
	logger.Info(
		"done",
		slog.Group("inner", slog.String("API-KEY", "secret"), slog.Time("at", time.Unix(1, 0))),
		slog.Any("resolved", groupValuer{}),
	)

	var record map[string]any
	decoder := json.NewDecoder(&output)
	decoder.UseNumber()
	if err := decoder.Decode(&record); err != nil {
		t.Fatal(err)
	}
	if record["value"] != json.Number("9007199254740993") {
		t.Fatalf("value = %#v", record["value"])
	}
	outer := record["outer"].(map[string]any)
	inner := outer["inner"].(map[string]any)
	if inner["API-KEY"] != redactedValue {
		t.Fatalf("API-KEY = %#v", inner["API-KEY"])
	}
	resolved := outer["resolved"].(map[string]any)
	if resolved["client-secret"] != redactedValue || resolved["name"] != "visible" {
		t.Fatalf("resolved = %#v", resolved)
	}
}

func TestSanitizingHandlerDelegatesLevelAndErrors(t *testing.T) {
	expected := errors.New("write failed")
	next := &failureHandler{err: expected, enabled: true}
	handler, err := NewSanitizingHandler(next, HandlerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !handler.Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("Enabled 没有委托给下游 Handler")
	}
	record := slog.NewRecord(time.Now(), slog.LevelInfo, "message", 0)
	if err := handler.Handle(context.Background(), record); !errors.Is(err, expected) {
		t.Fatalf("Handle() error = %v", err)
	}

	next.enabled = false
	if handler.Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("禁用等级仍被启用")
	}
	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatalf("禁用记录返回错误: %v", err)
	}
	if next.handled != 1 {
		t.Fatalf("handled = %d", next.handled)
	}
}

func TestSanitizingHandlerIsolatesPanics(t *testing.T) {
	next := &failureHandler{enabled: true, panics: true}
	handler, err := NewSanitizingHandler(next, HandlerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	record := slog.NewRecord(time.Now(), slog.LevelInfo, "message", 0)
	if err := handler.Handle(context.Background(), record); !errors.Is(err, ErrHandlerPanic) {
		t.Fatalf("Handle() error = %v", err)
	} else if err.Error() != ErrHandlerPanic.Error() {
		t.Fatalf("Handler panic 泄露了内部文本: %v", err)
	}

	handler, err = NewSanitizingHandler(slog.NewJSONHandler(&bytes.Buffer{}, nil), HandlerOptions{
		ContextAttrs: func(context.Context) []slog.Attr { panic("context token") },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.Handle(context.Background(), record); !errors.Is(err, ErrContextAttrsPanic) {
		t.Fatalf("ContextAttrs panic error = %v", err)
	} else if err.Error() != ErrContextAttrsPanic.Error() {
		t.Fatalf("ContextAttrs panic 泄露了内部文本: %v", err)
	}
}

func TestSanitizingHandlerRejectsInvalidConfiguration(t *testing.T) {
	var nilHandler *typedNilHandler
	for name, testCase := range map[string]struct {
		next    slog.Handler
		options HandlerOptions
	}{
		"nil":             {next: nil},
		"typed nil":       {next: nilHandler},
		"message limit":   {next: slog.NewJSONHandler(&bytes.Buffer{}, nil), options: HandlerOptions{MaxMessageBytes: 1}},
		"string limit":    {next: slog.NewJSONHandler(&bytes.Buffer{}, nil), options: HandlerOptions{MaxStringBytes: 1}},
		"attribute limit": {next: slog.NewJSONHandler(&bytes.Buffer{}, nil), options: HandlerOptions{MaxAttributes: -1}},
		"depth limit":     {next: slog.NewJSONHandler(&bytes.Buffer{}, nil), options: HandlerOptions{MaxDepth: -1}},
		"sensitive key":   {next: slog.NewJSONHandler(&bytes.Buffer{}, nil), options: HandlerOptions{ExtraSensitiveKeys: []string{"---"}}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewSanitizingHandler(testCase.next, testCase.options); err == nil {
				t.Fatal("期望构造失败")
			}
		})
	}
}

func TestSanitizingHandlerLimitsAttributesAndRecursiveValues(t *testing.T) {
	var output bytes.Buffer
	handler, err := NewSanitizingHandler(slog.NewJSONHandler(&output, nil), HandlerOptions{
		MaxAttributes: 2,
		MaxDepth:      2,
	})
	if err != nil {
		t.Fatal(err)
	}
	value := &recursiveValue{}
	value.Child = value
	slog.New(handler).Info("done", "one", 1, "two", 2, "three", 3, "recursive", value)

	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	if _, ok := record["three"]; ok {
		t.Fatal("属性上限后仍输出后续字段")
	}
}
