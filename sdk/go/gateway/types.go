// Package gateway 提供可声明组装且安全顺序固定的 HTTP 网关处理器。
package gateway

import (
	"context"
	"net/http"
	"time"
)

// AccessMode 描述路由是否需要认证身份。
type AccessMode uint8

const (
	// AccessProtected 是路由的安全默认值，要求请求通过认证。
	AccessProtected AccessMode = iota
	// AccessPublic 显式声明不需要认证的公开路由。
	AccessPublic
)

// RouteMatch 描述一个静态路由的匹配条件。
type RouteMatch struct {
	Methods    []string
	ExactPath  string
	PathPrefix string
}

// Route 描述请求的稳定路由结果。
type Route struct {
	Name         string
	Match        RouteMatch
	Upstream     string
	Access       AccessMode
	MaxBodyBytes int64
}

// Upstream 描述一个在网关启动阶段解析的固定上游。
type Upstream struct {
	Name string
	URL  string
}

// RouteResolver 把请求解析为一个项目声明的路由。
type RouteResolver interface {
	Resolve(*http.Request) (Route, bool, error)
}

// RouteResolverFunc 让函数直接实现 RouteResolver。
type RouteResolverFunc func(*http.Request) (Route, bool, error)

// Resolve 调用路由解析函数。
func (resolver RouteResolverFunc) Resolve(r *http.Request) (Route, bool, error) {
	return resolver(r)
}

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

// Authorize 调用授权函数。
func (authorize AuthorizerFunc) Authorize(ctx context.Context, r *http.Request, request RequestContext) (PolicyDecision, error) {
	return authorize(ctx, r, request)
}

// BeforeProxyPolicy 在安全阶段完成后执行最后的项目转发决策。
type BeforeProxyPolicy interface {
	Evaluate(context.Context, *http.Request, RequestContext) (PolicyDecision, error)
}

// BeforeProxyPolicyFunc 让函数直接实现 BeforeProxyPolicy。
type BeforeProxyPolicyFunc func(context.Context, *http.Request, RequestContext) (PolicyDecision, error)

// Evaluate 调用转发前策略函数。
func (policy BeforeProxyPolicyFunc) Evaluate(ctx context.Context, r *http.Request, request RequestContext) (PolicyDecision, error) {
	return policy(ctx, r, request)
}

// RateLimitScope 标识限流键的安全语义。
type RateLimitScope string

const (
	RateLimitScopeClientIP RateLimitScope = "client_ip"
	RateLimitScopeUserID   RateLimitScope = "user_id"
	RateLimitScopeUpstream RateLimitScope = "upstream"
)

// RateLimitRequest 是传给限流组件的规范输入。
type RateLimitRequest struct {
	Scope    RateLimitScope
	Key      string
	Route    string
	Upstream string
}

// RateLimitDecision 描述一次正常的限流决策。
type RateLimitDecision struct {
	Allowed    bool
	Remaining  int64
	RetryAfter time.Duration
	Reason     string
}

// RateLimiter 返回错误时，流水线按 503 拒绝请求而不是放行。
type RateLimiter interface {
	Allow(context.Context, RateLimitRequest) (RateLimitDecision, error)
}

// RateLimiterFunc 让函数直接实现 RateLimiter。
type RateLimiterFunc func(context.Context, RateLimitRequest) (RateLimitDecision, error)

// Allow 调用限流函数。
func (limiter RateLimiterFunc) Allow(ctx context.Context, request RateLimitRequest) (RateLimitDecision, error) {
	return limiter(ctx, request)
}

// ClientIPResolver 解析经过信任边界约束的客户端地址。
type ClientIPResolver interface {
	Resolve(*http.Request) (string, error)
}

// ClientIPResolverFunc 让函数直接实现 ClientIPResolver。
type ClientIPResolverFunc func(*http.Request) (string, error)

// Resolve 调用客户端地址解析函数。
func (resolver ClientIPResolverFunc) Resolve(r *http.Request) (string, error) {
	return resolver(r)
}

// UpstreamResolver 在写响应前为路由返回已经构造好的代理处理器。
type UpstreamResolver interface {
	ResolveUpstream(Route) (http.Handler, error)
}

// UpstreamResolverFunc 让函数直接实现 UpstreamResolver。
type UpstreamResolverFunc func(Route) (http.Handler, error)

// ResolveUpstream 调用上游解析函数。
func (resolver UpstreamResolverFunc) ResolveUpstream(route Route) (http.Handler, error) {
	return resolver(route)
}

// RequestIDConfig 控制传入请求 ID 的信任范围和生成方式。
type RequestIDConfig struct {
	Header    string
	MaxLength int
	Generate  func() (string, error)
}

// CORSConfig 控制网关拥有的浏览器跨域策略。
type CORSConfig struct {
	AllowedOrigins   []string
	AllowedMethods   []string
	AllowedHeaders   []string
	AllowCredentials bool
	MaxAge           time.Duration
}

// GatewayError 是错误响应器可安全暴露的网关错误。
type GatewayError struct {
	Status     int
	Code       string
	Message    string
	RetryAfter time.Duration
	Cause      error
}

// ErrorResponder 把网关错误写入 HTTP 响应。
type ErrorResponder interface {
	Respond(http.ResponseWriter, *http.Request, GatewayError)
}

// ErrorResponderFunc 让函数直接实现 ErrorResponder。
type ErrorResponderFunc func(http.ResponseWriter, *http.Request, GatewayError)

// Respond 调用错误响应函数。
func (responder ErrorResponderFunc) Respond(w http.ResponseWriter, r *http.Request, gatewayError GatewayError) {
	responder(w, r, gatewayError)
}
