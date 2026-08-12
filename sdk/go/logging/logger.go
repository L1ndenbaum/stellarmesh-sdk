package logging

import (
	"context"
	"errors"
	"strings"
	"time"
)

// Emitter 接收一个事件，不阻塞等待远端投递。
type Emitter interface {
	Emit(context.Context, Event) bool
}

// TraceIDProvider 从调用方管理的上下文状态中解析 trace id。
type TraceIDProvider func(context.Context) string

// LoggerConfig 配置面向应用的日志记录器。
type LoggerConfig struct {
	Service         string
	Emitter         Emitter
	Now             func() time.Time
	TraceIDProvider TraceIDProvider
}

// Logger 为单个服务构造结构化事件。
type Logger struct {
	service         string
	emitter         Emitter
	now             func() time.Time
	traceIDProvider TraceIDProvider
}

// NewLogger 创建结构化日志门面。
func NewLogger(config LoggerConfig) (*Logger, error) {
	if strings.TrimSpace(config.Service) == "" {
		return nil, errors.New("logging service name is required")
	}
	if config.Emitter == nil {
		return nil, errors.New("logging emitter is required")
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	return &Logger{service: config.Service, emitter: config.Emitter, now: now, traceIDProvider: config.TraceIDProvider}, nil
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
