package sessionauth

import (
	"errors"
	"strings"
)

// KeyConfig 配置统一分隔符；空值使用冒号。
type KeyConfig struct{ Separator string }

// KeyBuilder 按项目隔离键空间，并转义标识中的特殊字节。
// 零值等价于默认分隔符。
type KeyBuilder struct{ separator string }

// NewKeyBuilder 接受不含百分号和大括号的 ASCII 标点分隔符。
func NewKeyBuilder(config KeyConfig) (*KeyBuilder, error) {
	separator := config.Separator
	if separator == "" {
		separator = ":"
	}
	for _, c := range separator {
		if c < 33 || c > 126 || c == '%' || c == '{' || c == '}' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
			return nil, errors.New("invalid session key separator")
		}
	}
	return &KeyBuilder{separator: separator}, nil
}

// SessionKey 拼接项目 scope 和 Session ID，不要求调用方手写固定段。
func (builder KeyBuilder) SessionKey(projectScope, sessionID string) (string, error) {
	return builder.key(projectScope, "session", sessionID)
}

// UserSessionsKey 拼接项目 scope 和用户 ID，用于会话反查。
func (builder KeyBuilder) UserSessionsKey(projectScope, userID string) (string, error) {
	return builder.key(projectScope, "user_sessions", userID)
}

func (builder KeyBuilder) delimiter() string {
	if builder.separator == "" {
		return ":"
	}
	return builder.separator
}
func (builder KeyBuilder) key(scope, kind, id string) (string, error) {
	if scope == "" || id == "" {
		return "", errors.New("session key scope and ID are required")
	}
	return builder.prefix(scope, kind) + builder.escape(id), nil
}
func (builder KeyBuilder) prefix(scope, kind string) string {
	return builder.escape(scope) + builder.delimiter() + kind + builder.delimiter()
}
func (builder KeyBuilder) escape(value string) string {
	const hex = "0123456789ABCDEF"
	var result strings.Builder
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c <= 32 || c >= 127 || c == '%' || c == '{' || c == '}' || strings.ContainsRune(builder.delimiter(), rune(c)) {
			result.WriteByte('%')
			result.WriteByte(hex[c>>4])
			result.WriteByte(hex[c&15])
		} else {
			result.WriteByte(c)
		}
	}
	return result.String()
}
