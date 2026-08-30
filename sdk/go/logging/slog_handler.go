package logging

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"strings"
	"time"
)

// SlogHandlerConfig 配置标准库 slog 到 Stellarmesh Event 的转换。
type SlogHandlerConfig struct {
	Service         string
	MinimumLevel    Level
	TraceIDProvider TraceIDProvider
	AddSource       bool
}

type scopedAttr struct {
	groups []string
	attr   slog.Attr
}

// SlogHandler 将 slog.Record 非阻塞地交给已有 Emitter。
type SlogHandler struct {
	emitter Emitter
	config  SlogHandlerConfig
	attrs   []scopedAttr
	groups  []string
}

// NewSlogHandler 创建标准库 slog Handler。
func NewSlogHandler(emitter Emitter, config SlogHandlerConfig) (slog.Handler, error) {
	if emitter == nil {
		return nil, errors.New("logging slog emitter is required")
	}
	if strings.TrimSpace(config.Service) == "" || config.Service != strings.TrimSpace(config.Service) {
		return nil, errors.New("logging slog service name must be non-empty and trimmed")
	}
	if config.MinimumLevel == "" {
		config.MinimumLevel = LevelInfo
	}
	if err := config.MinimumLevel.Validate(); err != nil {
		return nil, err
	}
	return &SlogHandler{emitter: emitter, config: config}, nil
}

// Enabled 在构造 Event 和清洗 metadata 前执行级别过滤。
func (handler *SlogHandler) Enabled(_ context.Context, level slog.Level) bool {
	return levelEnabled(mapSlogLevel(level), handler.config.MinimumLevel)
}

// Handle 转换一条 slog 记录并交给异步 Emitter。
func (handler *SlogHandler) Handle(ctx context.Context, record slog.Record) error {
	if !handler.Enabled(ctx, record.Level) {
		return nil
	}
	metadata := make(map[string]any)
	traceID := ""
	for _, scoped := range handler.attrs {
		addSlogAttr(metadata, scoped.groups, scoped.attr, &traceID)
	}
	record.Attrs(func(attr slog.Attr) bool {
		addSlogAttr(metadata, handler.groups, attr, &traceID)
		return true
	})
	if traceID == "" && handler.config.TraceIDProvider != nil {
		traceID = handler.config.TraceIDProvider(ctx)
	}
	if handler.config.AddSource && record.PC != 0 {
		frame, _ := runtime.CallersFrames([]uintptr{record.PC}).Next()
		metadata["source"] = map[string]any{"file": frame.File, "line": frame.Line}
	}
	timestamp := record.Time
	if timestamp.IsZero() {
		timestamp = time.Now()
	}
	handler.emitter.Emit(ctx, Event{
		Timestamp: timestamp.UTC(), Kind: EventKindLog, Level: mapSlogLevel(record.Level), Service: handler.config.Service,
		Message: record.Message, TraceID: traceID, Metadata: SanitizeMetadata(metadata),
	})
	return nil
}

// WithAttrs 返回携带固定属性的独立 Handler。
func (handler *SlogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	cloned := handler.clone()
	for _, attr := range attrs {
		cloned.attrs = append(cloned.attrs, scopedAttr{groups: append([]string(nil), handler.groups...), attr: attr})
	}
	return cloned
}

// WithGroup 返回把后续属性放入指定组的独立 Handler。
func (handler *SlogHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return handler
	}
	cloned := handler.clone()
	cloned.groups = append(cloned.groups, name)
	return cloned
}

func (handler *SlogHandler) clone() *SlogHandler {
	return &SlogHandler{
		emitter: handler.emitter, config: handler.config,
		attrs: append([]scopedAttr(nil), handler.attrs...), groups: append([]string(nil), handler.groups...),
	}
}

func mapSlogLevel(level slog.Level) Level {
	switch {
	case level < slog.LevelInfo:
		return LevelDebug
	case level < slog.LevelWarn:
		return LevelInfo
	case level < slog.LevelError:
		return LevelWarning
	default:
		return LevelError
	}
}

func addSlogAttr(metadata map[string]any, groups []string, attr slog.Attr, traceID *string) {
	attr.Value = attr.Value.Resolve()
	if attr.Equal(slog.Attr{}) {
		return
	}
	if attr.Value.Kind() == slog.KindGroup {
		nestedGroups := groups
		if attr.Key != "" {
			nestedGroups = append(append([]string(nil), groups...), attr.Key)
		}
		for _, child := range attr.Value.Group() {
			addSlogAttr(metadata, nestedGroups, child, traceID)
		}
		return
	}
	if len(groups) == 0 && attr.Key == "trace_id" {
		*traceID = fmt.Sprint(slogValue(attr.Value))
		return
	}
	target := metadata
	for _, group := range groups {
		nested, ok := target[group].(map[string]any)
		if !ok {
			nested = make(map[string]any)
			target[group] = nested
		}
		target = nested
	}
	target[attr.Key] = slogValue(attr.Value)
}

func slogValue(value slog.Value) any {
	switch value.Kind() {
	case slog.KindBool:
		return value.Bool()
	case slog.KindDuration:
		return value.Duration()
	case slog.KindFloat64:
		return value.Float64()
	case slog.KindInt64:
		return value.Int64()
	case slog.KindString:
		return value.String()
	case slog.KindTime:
		return value.Time()
	case slog.KindUint64:
		return value.Uint64()
	default:
		return value.Any()
	}
}
