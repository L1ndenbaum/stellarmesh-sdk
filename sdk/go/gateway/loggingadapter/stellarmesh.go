// Package loggingadapter 提供 Gateway 访问记录到具体日志实现的可选适配器。
package loggingadapter

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"

	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/gateway"
	sharedlogging "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging"
)

// ErrEventRejected 表示 Emitter 没有接收访问日志事件。
var ErrEventRejected = errors.New("Stellarmesh Logging 拒绝了 Gateway 日志事件")

// TraceIDProvider 从请求上下文和访问记录中解析真实的链路标识。
type TraceIDProvider func(context.Context, gateway.AccessLog) string

// StellarmeshConfig 配置 Gateway 到 Stellarmesh Logging 的数据转换。
type StellarmeshConfig struct {
	Service         string
	Emitter         sharedlogging.Emitter
	IncludeIdentity bool
	TraceIDProvider TraceIDProvider
}

type stellarmeshAccessLogger struct {
	service         string
	emitter         sharedlogging.Emitter
	includeIdentity bool
	traceIDProvider TraceIDProvider
}

// NewStellarmesh 创建只负责转换和提交事件的访问日志适配器。
func NewStellarmesh(config StellarmeshConfig) (gateway.AccessLogger, error) {
	if strings.TrimSpace(config.Service) == "" || config.Service != strings.TrimSpace(config.Service) {
		return nil, errors.New("gateway logging service must be non-empty and trimmed")
	}
	if isNilInterface(config.Emitter) {
		return nil, errors.New("gateway logging emitter is nil")
	}
	return &stellarmeshAccessLogger{
		service:         config.Service,
		emitter:         config.Emitter,
		includeIdentity: config.IncludeIdentity,
		traceIDProvider: config.TraceIDProvider,
	}, nil
}

func (logger *stellarmeshAccessLogger) Log(ctx context.Context, accessLog gateway.AccessLog) error {
	metadata := map[string]any{
		"request_id":           accessLog.RequestID,
		"method":               accessLog.Method,
		"path":                 accessLog.Path,
		"route":                accessLog.Route,
		"client_ip":            accessLog.ClientIP,
		"auth_result":          accessLog.AuthResult,
		"upstream":             accessLog.Upstream,
		"status":               accessLog.Status,
		"elapsed_milliseconds": accessLog.Elapsed.Milliseconds(),
		"error_code":           accessLog.ErrorCode,
	}
	if logger.includeIdentity {
		metadata["user_id"] = accessLog.UserID
		metadata["roles"] = append([]string(nil), accessLog.Roles...)
	}
	rateLimitResults := make(map[string]string, len(accessLog.RateLimitResult))
	for scope, result := range accessLog.RateLimitResult {
		rateLimitResults[string(scope)] = result
	}
	metadata["rate_limit_result"] = rateLimitResults

	traceID := ""
	if logger.traceIDProvider != nil {
		traceID = logger.traceIDProvider(ctx, accessLog)
	}
	if !logger.emitter.Emit(ctx, sharedlogging.Event{
		Timestamp: accessLog.Timestamp.UTC(),
		Kind:      sharedlogging.EventKindLog,
		Level:     stellarmeshLevel(accessLog.Status),
		Service:   logger.service,
		Message:   "gateway request",
		TraceID:   traceID,
		Metadata:  sharedlogging.SanitizeMetadata(metadata),
	}) {
		return fmt.Errorf("emit gateway access log: %w", ErrEventRejected)
	}
	return nil
}

func stellarmeshLevel(status int) sharedlogging.Level {
	switch {
	case status >= http.StatusInternalServerError:
		return sharedlogging.LevelError
	case status >= http.StatusBadRequest:
		return sharedlogging.LevelWarning
	default:
		return sharedlogging.LevelInfo
	}
}

func isNilInterface(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
