package gateway

import (
	"errors"
)

// Option 声明一个网关组件；安全关键组件的执行顺序不由 Option 顺序决定。
type Option interface {
	apply(*config) error
}

type optionFunc func(*config) error

func (option optionFunc) apply(config *config) error {
	return option(config)
}

type config struct {
	routeResolver        RouteResolver
	staticRoutes         []Route
	upstreamResolver     UpstreamResolver
	authenticator        Authenticator
	authorizer           Authorizer
	beforeProxy          BeforeProxyPolicy
	clientIPResolver     ClientIPResolver
	clientIPLimiter      RateLimiter
	userLimiter          RateLimiter
	upstreamLimiter      RateLimiter
	errorResponder       ErrorResponder
	requestID            RequestIDConfig
	configuredComponents map[string]struct{}
}

// WithRoutes 使用经过启动校验的静态路由表。
func WithRoutes(routes ...Route) Option {
	return componentOption("routes", func(config *config) error {
		config.staticRoutes = append([]Route(nil), routes...)
		return nil
	})
}

// WithRouteResolver 使用项目提供的动态路由解析器。
func WithRouteResolver(resolver RouteResolver) Option {
	return componentOption("route_resolver", func(config *config) error {
		if resolver == nil {
			return errors.New("gateway route resolver is nil")
		}
		config.routeResolver = resolver
		return nil
	})
}

// WithUpstreamResolver 使用项目提供的上游处理器解析器。
func WithUpstreamResolver(resolver UpstreamResolver) Option {
	return componentOption("upstream_resolver", func(config *config) error {
		if resolver == nil {
			return errors.New("gateway upstream resolver is nil")
		}
		config.upstreamResolver = resolver
		return nil
	})
}

// WithAuthenticator 启用受保护路由认证。
func WithAuthenticator(authenticator Authenticator) Option {
	return componentOption("authenticator", func(config *config) error {
		if authenticator == nil {
			return errors.New("gateway authenticator is nil")
		}
		config.authenticator = authenticator
		return nil
	})
}

// WithAuthorizer 启用认证后的项目授权策略。
func WithAuthorizer(authorizer Authorizer) Option {
	return componentOption("authorizer", func(config *config) error {
		if authorizer == nil {
			return errors.New("gateway authorizer is nil")
		}
		config.authorizer = authorizer
		return nil
	})
}

// WithBeforeProxyPolicy 启用转发前的项目策略检查。
func WithBeforeProxyPolicy(policy BeforeProxyPolicy) Option {
	return componentOption("before_proxy_policy", func(config *config) error {
		if policy == nil {
			return errors.New("gateway before-proxy policy is nil")
		}
		config.beforeProxy = policy
		return nil
	})
}

// WithClientIPResolver 覆盖默认仅信任 RemoteAddr 的客户端地址解析器。
func WithClientIPResolver(resolver ClientIPResolver) Option {
	return componentOption("client_ip_resolver", func(config *config) error {
		if resolver == nil {
			return errors.New("gateway client IP resolver is nil")
		}
		config.clientIPResolver = resolver
		return nil
	})
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

// WithErrorResponder 覆盖默认的 Stellarmesh JSON 错误响应。
func WithErrorResponder(responder ErrorResponder) Option {
	return componentOption("error_responder", func(config *config) error {
		if responder == nil {
			return errors.New("gateway error responder is nil")
		}
		config.errorResponder = responder
		return nil
	})
}

// WithRequestID 配置请求 ID；空字段使用 SDK 安全默认值。
func WithRequestID(requestID RequestIDConfig) Option {
	return componentOption("request_id", func(config *config) error {
		config.requestID = requestID
		return nil
	})
}

func componentOption(name string, configure func(*config) error) Option {
	return optionFunc(func(config *config) error {
		if _, exists := config.configuredComponents[name]; exists {
			return errors.New("duplicate gateway component: " + name)
		}
		config.configuredComponents[name] = struct{}{}
		return configure(config)
	})
}

func rateLimiterOption(name string, limiter RateLimiter, assign func(*config, RateLimiter)) Option {
	return componentOption(name, func(config *config) error {
		if limiter == nil {
			return errors.New("gateway rate limiter is nil")
		}
		assign(config, limiter)
		return nil
	})
}
