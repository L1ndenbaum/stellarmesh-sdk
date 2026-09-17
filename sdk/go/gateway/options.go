package gateway

import (
	"errors"
	"net/http"
	"reflect"
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
	credentialExtractor  CredentialExtractor
	authorizer           Authorizer
	beforeProxy          BeforeProxyPolicy
	clientIPResolver     ClientIPResolver
	clientIPLimiter      RateLimiter
	userLimiter          RateLimiter
	upstreamLimiter      RateLimiter
	errorResponder       ErrorResponder
	requestID            RequestIDConfig
	upstreamSpecs        []Upstream
	transport            http.RoundTripper
	cors                 *CORSConfig
	accessLogger         AccessLogger
	accessLogDisabled    bool
	observer             Observer
	health               *HealthConfig
	configuredComponents map[string]struct{}
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

func isNilInterface(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
