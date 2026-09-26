package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
)

const headerXRequestID = "X-Request-ID"

// RequestIDConfig 控制传入请求 ID 的信任范围和生成方式。
type RequestIDConfig struct {
	// Header 留空使用 X-Request-ID；需合法 HTTP 字段名。
	Header string
	// MaxLength 为最大字节数，0 使用 128；显式值范围 16 至 1024。
	MaxLength int
	// Generate 为 nil 时生成 16 字节随机值的十六进制文本；返回值仍须通过长度和字符校验。
	Generate func() (string, error)
	// TrustIncoming 允许沿用传入请求中的单个合法 ID；默认 false，始终重新生成。
	// 开启不会验证来源，调用方须保证入口和前置代理的头部处理可信。
	// 缺失、非法或重复的传入值仍由 Generate 替换，与 WithTrustedProxies 无关。
	TrustIncoming bool
}

const defaultRequestIDMaxLength = 128

// WithRequestID 配置请求 ID；默认重新生成，只有 TrustIncoming 为 true 才沿用合法传入值。
func WithRequestID(requestID RequestIDConfig) Option {
	return componentOption("request_id", func(config *config) error {
		config.requestID = requestID
		return nil
	})
}

func normalizeRequestIDConfig(config RequestIDConfig) (RequestIDConfig, error) {
	config.Header = strings.TrimSpace(config.Header)
	if config.Header == "" {
		config.Header = headerXRequestID
	}
	if !isHTTPToken(config.Header) {
		return RequestIDConfig{}, errors.New("gateway request ID header is invalid")
	}
	if config.MaxLength == 0 {
		config.MaxLength = defaultRequestIDMaxLength
	}
	if config.MaxLength < 16 || config.MaxLength > 1024 {
		return RequestIDConfig{}, errors.New("gateway request ID max length must be between 16 and 1024")
	}
	if config.Generate == nil {
		config.Generate = generateRequestID
	}
	return config, nil
}

func (gateway *Gateway) resolveRequestID(r *http.Request) (string, error) {
	values := r.Header.Values(gateway.requestID.Header)
	// 先移除传入值，即使生成失败，也不向后续错误响应器暴露未经采纳的请求 ID。
	r.Header.Del(gateway.requestID.Header)
	if gateway.requestID.TrustIncoming && len(values) == 1 {
		value := strings.TrimSpace(values[0])
		if isSafeRequestID(value, gateway.requestID.MaxLength) {
			return value, nil
		}
	}
	value, err := gateway.requestID.Generate()
	if err != nil {
		return "", err
	}
	if !isSafeRequestID(value, gateway.requestID.MaxLength) {
		return "", errors.New("request ID generator returned an invalid value")
	}
	return value, nil
}

func isSafeRequestID(value string, maxLength int) bool {
	if value == "" || len(value) > maxLength {
		return false
	}
	for _, character := range value {
		if character < 0x21 || character > 0x7e {
			return false
		}
	}
	return true
}

func generateRequestID() (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return hex.EncodeToString(random), nil
}

// RequestIDFromContext 返回 SDK 写入请求上下文的请求 ID。
func RequestIDFromContext(ctx context.Context) (string, bool) {
	requestID, ok := ctx.Value(requestIDContextKey).(string)
	return requestID, ok
}
