package gateway

import (
	"context"
	"errors"
	"net/http"
)

// PolicyDecision 描述授权或转发前策略的正常决策。
type PolicyDecision struct {
	Allowed bool
	Reason  string
}

// RequestContext 向类型化策略暴露只读的网关请求状态。
type RequestContext struct {
	RequestID string
	ClientIP  string
	Route     Route
	Identity  *Identity
}

// Authorizer 在认证和用户限流后执行项目授权。
type Authorizer interface {
	Authorize(context.Context, *http.Request, RequestContext) (PolicyDecision, error)
}

// AuthorizerFunc 让函数直接实现 Authorizer。
type AuthorizerFunc func(context.Context, *http.Request, RequestContext) (PolicyDecision, error)

// BeforeProxyPolicy 在安全阶段完成后执行最后的项目转发决策。
type BeforeProxyPolicy interface {
	Evaluate(context.Context, *http.Request, RequestContext) (PolicyDecision, error)
}

// BeforeProxyPolicyFunc 让函数直接实现 BeforeProxyPolicy。
type BeforeProxyPolicyFunc func(context.Context, *http.Request, RequestContext) (PolicyDecision, error)

// Authorize 调用授权函数。
func (authorize AuthorizerFunc) Authorize(ctx context.Context, r *http.Request, request RequestContext) (PolicyDecision, error) {
	return authorize(ctx, r, request)
}

// Evaluate 调用转发前策略函数。
func (policy BeforeProxyPolicyFunc) Evaluate(ctx context.Context, r *http.Request, request RequestContext) (PolicyDecision, error) {
	return policy(ctx, r, request)
}

// WithAuthorizer 启用认证后的项目授权策略。
func WithAuthorizer(authorizer Authorizer) Option {
	return componentOption("authorizer", func(config *config) error {
		if isNilInterface(authorizer) {
			return errors.New("gateway authorizer is nil")
		}
		config.authorizer = authorizer
		return nil
	})
}

// WithBeforeProxyPolicy 启用转发前的项目策略检查。
func WithBeforeProxyPolicy(policy BeforeProxyPolicy) Option {
	return componentOption("before_proxy_policy", func(config *config) error {
		if isNilInterface(policy) {
			return errors.New("gateway before-proxy policy is nil")
		}
		config.beforeProxy = policy
		return nil
	})
}
