package logging

import (
	"context"
	"log/slog"
	"testing"
)

type groupedLogValue struct{}

func (groupedLogValue) LogValue() slog.Value {
	return slog.GroupValue(slog.String("apiKey", "secret"), slog.String("safe", "value"))
}

func TestSlogHandlerMapsLevelsAndFiltersBeforeEmission(t *testing.T) {
	emitter := &recordingEmitter{}
	handler, err := NewSlogHandler(emitter, SlogHandlerConfig{Service: "backend", MinimumLevel: LevelWarning})
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(handler)
	logger.Debug("debug")
	logger.Info("info")
	logger.Warn("warning")
	logger.Error("error")
	logger.Log(context.Background(), SlogLevelAudit, "audit")
	if len(emitter.events) != 3 {
		t.Fatalf("events = %#v", emitter.events)
	}
	levels := []Level{emitter.events[0].Level, emitter.events[1].Level, emitter.events[2].Level}
	want := []Level{LevelWarning, LevelError, LevelAudit}
	for index := range want {
		if levels[index] != want[index] {
			t.Fatalf("levels = %v", levels)
		}
	}
}

func TestSlogHandlerPreservesGroupsAndExtractsTraceID(t *testing.T) {
	emitter := &recordingEmitter{}
	handler, err := NewSlogHandler(emitter, SlogHandlerConfig{
		Service: "backend", TraceIDProvider: func(context.Context) string { return "provider-trace" }, AddSource: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler = handler.WithAttrs([]slog.Attr{
		slog.String("trace_id", "attribute-trace"),
		slog.String("service", "cannot-override"),
		slog.Any("fixed", groupedLogValue{}),
	}).WithGroup("request")
	slog.New(handler).Info("handled", slog.Int64("id", 9_007_199_254_740_993))
	if len(emitter.events) != 1 {
		t.Fatalf("events = %#v", emitter.events)
	}
	event := emitter.events[0]
	if event.Service != "backend" || event.TraceID != "attribute-trace" {
		t.Fatalf("event identity = %#v", event)
	}
	if event.Metadata["service"] != "cannot-override" {
		t.Fatalf("service metadata = %#v", event.Metadata)
	}
	fixed := event.Metadata["fixed"].(map[string]any)
	if fixed["apiKey"] != redactedValue || fixed["safe"] != "value" {
		t.Fatalf("fixed metadata = %#v", fixed)
	}
	request := event.Metadata["request"].(map[string]any)
	if request["id"] != int64(9_007_199_254_740_993) {
		t.Fatalf("request metadata = %#v", request)
	}
	if _, ok := event.Metadata["source"].(map[string]any); !ok {
		t.Fatalf("source metadata = %#v", event.Metadata["source"])
	}
}

func TestSlogHandlerUsesProviderWhenTraceAttributeIsAbsent(t *testing.T) {
	emitter := &recordingEmitter{}
	handler, err := NewSlogHandler(emitter, SlogHandlerConfig{
		Service: "backend", TraceIDProvider: func(context.Context) string { return "provider-trace" },
	})
	if err != nil {
		t.Fatal(err)
	}
	slog.New(handler).InfoContext(context.Background(), "handled")
	if len(emitter.events) != 1 || emitter.events[0].TraceID != "provider-trace" {
		t.Fatalf("events = %#v", emitter.events)
	}
}

func TestSlogHandlerAndLoggerValidateMinimumLevels(t *testing.T) {
	if _, err := NewSlogHandler(nil, SlogHandlerConfig{Service: "backend"}); err == nil {
		t.Fatal("NewSlogHandler() accepted nil emitter")
	}
	if _, err := NewSlogHandler(&recordingEmitter{}, SlogHandlerConfig{Service: " backend "}); err == nil {
		t.Fatal("NewSlogHandler() accepted untrimmed service")
	}
	if _, err := NewSlogHandler(&recordingEmitter{}, SlogHandlerConfig{Service: "backend", MinimumLevel: "NOTICE"}); err == nil {
		t.Fatal("NewSlogHandler() accepted invalid level")
	}
	emitter := &recordingEmitter{}
	logger, err := NewLogger(LoggerConfig{Service: "backend", Emitter: emitter, MinimumLevel: LevelError})
	if err != nil {
		t.Fatal(err)
	}
	if logger.Info(context.Background(), "filtered", "", nil) {
		t.Fatal("Info() was not filtered")
	}
	if !logger.Error(context.Background(), "emitted", "", nil) || len(emitter.events) != 1 {
		t.Fatalf("events = %#v", emitter.events)
	}
}
