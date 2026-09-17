package gateway

import (
	"context"
	"errors"
	"net/http"
	"time"
)

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

// WithClientIPRateLimiter 启用认证前的客户端 IP 限流。
func WithClientIPRateLimiter(limiter RateLimiter) Option {
	return rateLimiterOption("client_ip_limiter", limiter, func(config *config, limiter RateLimiter) {
		config.clientIPLimiter = limiter
	})
}

// WithUserRateLimiter 启用认证后的用户限流。
func WithUserRateLimiter(limiter RateLimiter) Option {
	return rateLimiterOption("user_limiter", limiter, func(config *config, limiter RateLimiter) {
		config.userLimiter = limiter
	})
}

// WithUpstreamRateLimiter 启用转发前的 upstream 限流。
func WithUpstreamRateLimiter(limiter RateLimiter) Option {
	return rateLimiterOption("upstream_limiter", limiter, func(config *config, limiter RateLimiter) {
		config.upstreamLimiter = limiter
	})
}

func rateLimiterOption(name string, limiter RateLimiter, assign func(*config, RateLimiter)) Option {
	return componentOption(name, func(config *config) error {
		if isNilInterface(limiter) {
			return errors.New("gateway rate limiter is nil")
		}
		assign(config, limiter)
		return nil
	})
}

func (gateway *Gateway) applyRateLimit(w http.ResponseWriter, r *http.Request, limiter RateLimiter, request RateLimitRequest) bool {
	if limiter == nil {
		setRateLimitResult(r, request.Scope, "disabled")
		return true
	}
	decision, err := limiter.Allow(r.Context(), request)
	if err != nil {
		setRateLimitResult(r, request.Scope, "error")
		gateway.fail(w, r, unavailableError("rate_limiter_unavailable", err))
		return false
	}
	if decision.Allowed {
		setRateLimitResult(r, request.Scope, "allowed")
		return true
	}
	setRateLimitResult(r, request.Scope, "rejected")
	gateway.fail(w, r, GatewayError{
		Status: http.StatusTooManyRequests, Code: "rate_limit_exceeded", Message: "rate limit exceeded", RetryAfter: decision.RetryAfter,
	})
	return false
}
