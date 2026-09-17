package gateway

import (
	"context"
	"errors"
)

// Identity 是网关确认后可以安全传给上游的调用方身份。
type Identity struct {
	UserID     string
	Roles      []string
	Attributes map[string]any
}

// AuthenticationDecision 区分凭据拒绝和认证组件自身故障。
type AuthenticationDecision struct {
	Authenticated bool
	Identity      Identity
	Reason        string
}

// Authenticator 校验 Bearer token；返回错误表示组件故障并触发 fail-close。
type Authenticator interface {
	Authenticate(context.Context, string) (AuthenticationDecision, error)
}

// AuthenticatorFunc 让函数直接实现 Authenticator。
type AuthenticatorFunc func(context.Context, string) (AuthenticationDecision, error)

// Authenticate 调用认证函数。
func (authenticate AuthenticatorFunc) Authenticate(ctx context.Context, token string) (AuthenticationDecision, error) {
	return authenticate(ctx, token)
}

// WithAuthenticator 启用受保护路由认证。
func WithAuthenticator(authenticator Authenticator) Option {
	return componentOption("authenticator", func(config *config) error {
		if isNilInterface(authenticator) {
			return errors.New("gateway authenticator is nil")
		}
		config.authenticator = authenticator
		return nil
	})
}
