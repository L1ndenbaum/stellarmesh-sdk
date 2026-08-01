package logging

import (
	"context"
	"time"
)

// Emitter accepts one event without blocking on remote delivery.
type Emitter interface {
	Emit(context.Context, Event) bool
}

// TraceIDProvider resolves a trace id from caller-owned context state.
type TraceIDProvider func(context.Context) string

// LoggerConfig configures an application-facing logger.
type LoggerConfig struct {
	Service         string
	Emitter         Emitter
	Now             func() time.Time
	TraceIDProvider TraceIDProvider
}

// Logger builds structured events for one service.
type Logger struct {
	service         string
	emitter         Emitter
	now             func() time.Time
	traceIDProvider TraceIDProvider
}

// NewLogger creates a structured logger facade.
func NewLogger(config LoggerConfig) *Logger {
	now := config.Now
	if now == nil {
		now = time.Now
	}
	return &Logger{service: config.Service, emitter: config.Emitter, now: now, traceIDProvider: config.TraceIDProvider}
}

func (logger *Logger) Debug(ctx context.Context, message, traceID string, metadata map[string]any) bool {
	return logger.emit(ctx, LevelDebug, message, traceID, metadata)
}

func (logger *Logger) Info(ctx context.Context, message, traceID string, metadata map[string]any) bool {
	return logger.emit(ctx, LevelInfo, message, traceID, metadata)
}

func (logger *Logger) Warning(ctx context.Context, message, traceID string, metadata map[string]any) bool {
	return logger.emit(ctx, LevelWarning, message, traceID, metadata)
}

func (logger *Logger) Error(ctx context.Context, message, traceID string, metadata map[string]any) bool {
	return logger.emit(ctx, LevelError, message, traceID, metadata)
}

func (logger *Logger) Audit(ctx context.Context, message, traceID string, metadata map[string]any) bool {
	return logger.emit(ctx, LevelAudit, message, traceID, metadata)
}

func (logger *Logger) emit(ctx context.Context, level Level, message, traceID string, metadata map[string]any) bool {
	if logger.emitter == nil {
		return false
	}
	if traceID == "" && logger.traceIDProvider != nil {
		traceID = logger.traceIDProvider(ctx)
	}
	return logger.emitter.Emit(ctx, Event{
		Timestamp: logger.now().UTC(),
		Level:     level, Service: logger.service, Message: message, TraceID: traceID,
		Metadata: SanitizeMetadata(metadata),
	})
}
