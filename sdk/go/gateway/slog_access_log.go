package gateway

import (
	"context"
	"log/slog"
	"net/http"
	"sort"
	"time"
)

const accessLogMessage = "gateway request completed"

// SlogAccessLoggerConfig 配置使用标准库 slog 输出的访问日志。
type SlogAccessLoggerConfig struct {
	// Logger 为空时在每次输出时读取 slog.Default。
	Logger          *slog.Logger
	IncludeIdentity bool
}

type slogAccessLogger struct {
	logger          *slog.Logger
	includeIdentity bool
}

// NewSlogAccessLogger 创建不拥有输出目标和生命周期的标准库访问日志实现。
func NewSlogAccessLogger(config SlogAccessLoggerConfig) AccessLogger {
	return &slogAccessLogger{
		logger:          config.Logger,
		includeIdentity: config.IncludeIdentity,
	}
}

// WithSlogAccessLogger 使用标准库 slog 输出访问日志。
func WithSlogAccessLogger(slogConfig SlogAccessLoggerConfig) Option {
	return componentOption("access_logger", func(gatewayConfig *config) error {
		gatewayConfig.accessLogger = NewSlogAccessLogger(slogConfig)
		return nil
	})
}

// WithoutAccessLog 显式关闭默认启用的访问日志。
func WithoutAccessLog() Option {
	return componentOption("access_logger", func(config *config) error {
		config.accessLogDisabled = true
		return nil
	})
}

func (logger *slogAccessLogger) Log(ctx context.Context, accessLog AccessLog) error {
	target := logger.logger
	if target == nil {
		target = slog.Default()
	}
	level := accessLogSlogLevel(accessLog.Status)
	if !target.Enabled(ctx, level) {
		return nil
	}

	record := slog.NewRecord(time.Now(), level, accessLogMessage, 0)
	record.AddAttrs(
		slog.String("request_id", accessLog.RequestID),
		slog.String("method", accessLog.Method),
		slog.String("path", accessLog.Path),
		slog.String("route", accessLog.Route),
		slog.String("client_ip", accessLog.ClientIP),
		slog.String("auth_result", accessLog.AuthResult),
		slog.String("upstream", accessLog.Upstream),
		slog.Int("status", accessLog.Status),
		slog.Int64("duration_ms", accessLog.Elapsed.Milliseconds()),
		slog.String("error_code", accessLog.ErrorCode),
	)
	if rateLimitResult := slogRateLimitResult(accessLog.RateLimitResult); !rateLimitResult.Equal(slog.Attr{}) {
		record.AddAttrs(rateLimitResult)
	}
	if logger.includeIdentity {
		record.AddAttrs(
			slog.String("user_id", accessLog.UserID),
			slog.Any("roles", append([]string(nil), accessLog.Roles...)),
		)
	}
	return target.Handler().Handle(ctx, record)
}

func accessLogSlogLevel(status int) slog.Level {
	switch {
	case status >= http.StatusInternalServerError:
		return slog.LevelError
	case status >= http.StatusBadRequest:
		return slog.LevelWarn
	default:
		return slog.LevelInfo
	}
}

func slogRateLimitResult(results map[RateLimitScope]string) slog.Attr {
	if len(results) == 0 {
		return slog.Attr{}
	}
	scopes := make([]string, 0, len(results))
	for scope := range results {
		scopes = append(scopes, string(scope))
	}
	sort.Strings(scopes)
	attributes := make([]any, 0, len(scopes))
	for _, scope := range scopes {
		attributes = append(attributes, slog.String(scope, results[RateLimitScope(scope)]))
	}
	return slog.Group("rate_limit_result", attributes...)
}
