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
	Logger *slog.Logger
	// IncludeIdentity 默认 false；开启后输出 user_id 与 roles，业务负责隐私策略。
	IncludeIdentity bool
	// UseRoutePath 以路径模板或稳定路由名记录 path；未匹配时使用 unmatched。
	UseRoutePath bool
}

type slogAccessLogger struct {
	logger          *slog.Logger
	includeIdentity bool
	useRoutePath    bool
}

// WithSlogAccessLogger 使用标准库 slog 输出访问日志。
func WithSlogAccessLogger(slogConfig SlogAccessLoggerConfig) Option {
	return componentOption("access_logger", func(gatewayConfig *config) error {
		gatewayConfig.accessLogger = NewSlogAccessLogger(slogConfig)
		return nil
	})
}

// NewSlogAccessLogger 创建不拥有输出目标和生命周期的标准库访问日志实现。
func NewSlogAccessLogger(config SlogAccessLoggerConfig) AccessLogger {
	return &slogAccessLogger{
		logger:          config.Logger,
		includeIdentity: config.IncludeIdentity,
		useRoutePath:    config.UseRoutePath,
	}
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

	path := accessLog.Path
	if logger.useRoutePath {
		switch {
		case accessLog.PathTemplate != "":
			path = accessLog.PathTemplate
		case accessLog.Route != "":
			path = accessLog.Route
		default:
			path = "unmatched"
		}
	}
	record := slog.NewRecord(time.Now(), level, accessLogMessage, 0)
	record.AddAttrs(
		slog.String("request_id", accessLog.RequestID),
		slog.String("method", accessLog.Method),
		slog.String("path", path),
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
