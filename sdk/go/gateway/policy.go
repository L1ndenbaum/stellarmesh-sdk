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
// Allowed=false 是正常拒绝（403），error 是组件故障（503），二者均不放行。
type Authorizer interface {
	Authorize(context.Context, *http.Request, RequestContext) (PolicyDecision, error)
}

// AuthorizerFunc 让函数直接实现 Authorizer。
type AuthorizerFunc func(context.Context, *http.Request, RequestContext) (PolicyDecision, error)

// BeforeProxyPolicy 在安全阶段完成后执行最后的项目转发决策。
// 正常拒绝返回 Allowed=false；组件故障返回 error，不应降级为放行。
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

func (gateway *Gateway) applyAuthorizer(w http.ResponseWriter, r *http.Request, request RequestContext) bool {
	if gateway.authorizer == nil {
		return true
	}
	decision, err := gateway.authorizer.Authorize(r.Context(), r, request)
	if err != nil {
		gateway.fail(w, r, unavailableError("authorization_failed", err))
		return false
	}
	if decision.Allowed {
		return true
	}
	gateway.fail(w, r, GatewayError{Status: http.StatusForbidden, Code: "forbidden", Message: "forbidden"})
	return false
}

func (gateway *Gateway) applyBeforeProxy(w http.ResponseWriter, r *http.Request, request RequestContext) bool {
	if gateway.beforeProxy == nil {
		return true
	}
	decision, err := gateway.beforeProxy.Evaluate(r.Context(), r, request)
	if err != nil {
		gateway.fail(w, r, unavailableError("before_proxy_policy_failed", err))
		return false
	}
	if decision.Allowed {
		return true
	}
	gateway.fail(w, r, GatewayError{Status: http.StatusForbidden, Code: "proxy_policy_rejected", Message: "forbidden"})
	return false
}
