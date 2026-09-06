package observability

import (
	"errors"
	"io"
	"log/slog"
	"strings"

	stellarlogging "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging"
)

// NewLogger 由服务入口选择格式与级别；对象存储 SDK 不拥有日志配置。
// 即使配置非法也返回可用 Logger，使启动失败保留正确级别与已识别的格式。
func NewLogger(output io.Writer, rawLevel, rawFormat string) (*slog.Logger, error) {
	level := slog.LevelInfo
	var configErr error
	switch strings.ToLower(strings.TrimSpace(rawLevel)) {
	case "", "info":
	case "debug":
		level = slog.LevelDebug
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		configErr = errors.New("LOG_LEVEL must be debug, info, warn, warning, or error")
	}
	options := &slog.HandlerOptions{Level: level}
	var base slog.Handler
	switch strings.ToLower(strings.TrimSpace(rawFormat)) {
	case "", "pretty":
		base = slog.NewTextHandler(output, options)
	case "json":
		base = slog.NewJSONHandler(output, options)
	default:
		base = slog.NewTextHandler(output, options)
		configErr = errors.Join(configErr, errors.New("LOG_FORMAT must be pretty or json"))
	}
	safe, err := stellarlogging.NewSanitizingHandler(base, stellarlogging.HandlerOptions{})
	if err != nil {
		return slog.New(base), errors.Join(configErr, err)
	}
	return slog.New(safe).With("service", "storage-service"), configErr
}
