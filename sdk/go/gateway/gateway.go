package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"strings"

	sharedapi "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/http/api"
	sharedheaders "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/http/headers"
)

const defaultRequestIDMaxLength = 128

type contextKey uint8

const (
	requestIDContextKey contextKey = iota
	routeContextKey
	identityContextKey
	clientIPContextKey
)

// Gateway 按固定的安全顺序执行项目声明的组件。
type Gateway struct {
	routes           RouteResolver
	upstreams        UpstreamResolver
	authenticator    Authenticator
	authorizer       Authorizer
	beforeProxy      BeforeProxyPolicy
	clientIPResolver ClientIPResolver
	clientIPLimiter  RateLimiter
	userLimiter      RateLimiter
	upstreamLimiter  RateLimiter
	errorResponder   ErrorResponder
	requestID        RequestIDConfig
	cors             *corsPolicy
}

// New 校验所有声明式组件并构造网关处理器。
func New(options ...Option) (*Gateway, error) {
	config := config{configuredComponents: make(map[string]struct{})}
	for index, option := range options {
		if option == nil {
			return nil, errors.New("gateway option at index " + itoa(index) + " is nil")
		}
		if err := option.apply(&config); err != nil {
			return nil, err
		}
	}
	if len(config.staticRoutes) > 0 && config.routeResolver != nil {
		return nil, errors.New("gateway static routes and route resolver are mutually exclusive")
	}
	if len(config.staticRoutes) > 0 {
		resolver, err := newStaticRouteResolver(config.staticRoutes)
		if err != nil {
			return nil, err
		}
		config.routeResolver = resolver
	}
	if config.routeResolver == nil {
		return nil, errors.New("gateway route resolver is required")
	}
	if len(config.upstreamSpecs) > 0 && config.upstreamResolver != nil {
		return nil, errors.New("gateway fixed upstreams and upstream resolver are mutually exclusive")
	}
	if config.errorResponder == nil {
		config.errorResponder = defaultErrorResponder{}
	}
	if len(config.upstreamSpecs) > 0 {
		resolver, err := newReverseProxyResolver(config.upstreamSpecs, config.transport, config.errorResponder)
		if err != nil {
			return nil, err
		}
		config.upstreamResolver = resolver
	} else if config.transport != nil {
		return nil, errors.New("gateway transport requires fixed upstreams")
	}
	if config.upstreamResolver == nil {
		return nil, errors.New("gateway upstream resolver is required")
	}
	if config.authenticator == nil && containsProtectedRoute(config.staticRoutes) {
		return nil, errors.New("gateway authenticator is required by protected routes")
	}
	if config.clientIPResolver == nil {
		config.clientIPResolver = ClientIPResolverFunc(resolveRemoteAddr)
	}
	requestID, err := normalizeRequestIDConfig(config.requestID)
	if err != nil {
		return nil, err
	}
	cors, err := newCORSPolicy(config.cors)
	if err != nil {
		return nil, err
	}
	if err := validateStaticUpstreams(config.staticRoutes, config.upstreamResolver); err != nil {
		return nil, err
	}
	return &Gateway{
		routes:           config.routeResolver,
		upstreams:        config.upstreamResolver,
		authenticator:    config.authenticator,
		authorizer:       config.authorizer,
		beforeProxy:      config.beforeProxy,
		clientIPResolver: config.clientIPResolver,
		clientIPLimiter:  config.clientIPLimiter,
		userLimiter:      config.userLimiter,
		upstreamLimiter:  config.upstreamLimiter,
		errorResponder:   config.errorResponder,
		requestID:        requestID,
		cors:             cors,
	}, nil
}

// ServeHTTP 执行固定流水线；任何转发决策组件错误都会停止请求。
func (gateway *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	recorder := newResponseRecorder(w)
	defer gateway.recoverPanic(recorder, r)

	requestID, err := gateway.resolveRequestID(r)
	if err != nil {
		gateway.fail(recorder, r, unavailableError("request_id_unavailable", err))
		return
	}
	r.Header.Set(gateway.requestID.Header, requestID)
	recorder.Header().Set(gateway.requestID.Header, requestID)
	r = r.WithContext(context.WithValue(r.Context(), requestIDContextKey, requestID))
	if gateway.cors != nil && gateway.cors.handle(recorder, r, gateway) {
		return
	}

	route, found, err := gateway.routes.Resolve(r)
	if err != nil {
		gateway.fail(recorder, r, unavailableError("route_resolution_failed", err))
		return
	}
	if !found {
		gateway.fail(recorder, r, GatewayError{Status: http.StatusNotFound, Code: "route_not_found", Message: "route not found"})
		return
	}
	route = cloneRoute(route)
	r = r.WithContext(context.WithValue(r.Context(), routeContextKey, route))
	upstream, err := gateway.upstreams.ResolveUpstream(route)
	if err != nil || upstream == nil {
		if err == nil {
			err = errors.New("gateway upstream handler is nil")
		}
		gateway.fail(recorder, r, unavailableError("upstream_resolution_failed", err))
		return
	}

	clientIP, err := gateway.clientIPResolver.Resolve(r)
	if err != nil || strings.TrimSpace(clientIP) == "" {
		if err == nil {
			err = errors.New("gateway client IP is empty")
		}
		gateway.fail(recorder, r, unavailableError("client_ip_resolution_failed", err))
		return
	}
	r = r.WithContext(context.WithValue(r.Context(), clientIPContextKey, clientIP))
	requestContext := RequestContext{RequestID: requestID, ClientIP: clientIP, Route: route}
	if !gateway.applyRateLimit(recorder, r, gateway.clientIPLimiter, RateLimitRequest{
		Scope: RateLimitScopeClientIP, Key: clientIP, Route: route.Name, Upstream: route.Upstream,
	}) {
		return
	}

	identity, ok := gateway.authenticate(recorder, r, route)
	if !ok {
		return
	}
	if identity != nil {
		requestContext.Identity = identity
		r = r.WithContext(context.WithValue(r.Context(), identityContextKey, cloneIdentity(*identity)))
		if !gateway.applyRateLimit(recorder, r, gateway.userLimiter, RateLimitRequest{
			Scope: RateLimitScopeUserID, Key: identity.UserID, Route: route.Name, Upstream: route.Upstream,
		}) {
			return
		}
	}
	if !gateway.applyAuthorizer(recorder, r, requestContext) {
		return
	}
	if !gateway.applyBeforeProxy(recorder, r, requestContext) {
		return
	}
	if !gateway.applyRateLimit(recorder, r, gateway.upstreamLimiter, RateLimitRequest{
		Scope: RateLimitScopeUpstream, Key: route.Upstream, Route: route.Name, Upstream: route.Upstream,
	}) {
		return
	}

	stripAndInjectIdentity(r.Header, identity)
	if route.MaxBodyBytes > 0 && r.Body != nil {
		if r.ContentLength > route.MaxBodyBytes {
			gateway.fail(recorder, r, GatewayError{Status: http.StatusRequestEntityTooLarge, Code: "request_body_too_large", Message: "request body too large"})
			return
		}
		r.Body = http.MaxBytesReader(recorder, r.Body, route.MaxBodyBytes)
	}
	upstream.ServeHTTP(recorder, r)
}

func (gateway *Gateway) authenticate(w http.ResponseWriter, r *http.Request, route Route) (*Identity, bool) {
	if route.Access == AccessPublic {
		return nil, true
	}
	if gateway.authenticator == nil {
		gateway.fail(w, r, unavailableError("authenticator_unavailable", errors.New("gateway authenticator is not configured")))
		return nil, false
	}
	raw := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(raw) <= len("Bearer ") || !strings.EqualFold(raw[:len("Bearer ")], "Bearer ") {
		gateway.fail(w, r, GatewayError{Status: http.StatusUnauthorized, Code: "missing_bearer_token", Message: "unauthorized"})
		return nil, false
	}
	token := strings.TrimSpace(raw[len("Bearer "):])
	if token == "" {
		gateway.fail(w, r, GatewayError{Status: http.StatusUnauthorized, Code: "missing_bearer_token", Message: "unauthorized"})
		return nil, false
	}
	decision, err := gateway.authenticator.Authenticate(r.Context(), token)
	if err != nil {
		gateway.fail(w, r, unavailableError("authentication_failed", err))
		return nil, false
	}
	if !decision.Authenticated || strings.TrimSpace(decision.Identity.UserID) == "" {
		gateway.fail(w, r, GatewayError{Status: http.StatusUnauthorized, Code: "invalid_bearer_token", Message: "unauthorized"})
		return nil, false
	}
	identity := cloneIdentity(decision.Identity)
	return &identity, true
}

func (gateway *Gateway) applyRateLimit(w http.ResponseWriter, r *http.Request, limiter RateLimiter, request RateLimitRequest) bool {
	if limiter == nil {
		return true
	}
	decision, err := limiter.Allow(r.Context(), request)
	if err != nil {
		gateway.fail(w, r, unavailableError("rate_limiter_unavailable", err))
		return false
	}
	if decision.Allowed {
		return true
	}
	gateway.fail(w, r, GatewayError{
		Status: http.StatusTooManyRequests, Code: "rate_limit_exceeded", Message: "rate limit exceeded", RetryAfter: decision.RetryAfter,
	})
	return false
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

func (gateway *Gateway) recoverPanic(w *responseRecorder, r *http.Request) {
	if recovered := recover(); recovered != nil && !w.WroteHeader() {
		gateway.fail(w, r, GatewayError{Status: http.StatusInternalServerError, Code: "gateway_panic", Message: "internal server error", Cause: errors.New("gateway component panic")})
	}
}

func (gateway *Gateway) fail(w http.ResponseWriter, r *http.Request, gatewayError GatewayError) {
	gateway.errorResponder.Respond(w, r, gatewayError)
}

func unavailableError(code string, cause error) GatewayError {
	return GatewayError{Status: http.StatusServiceUnavailable, Code: code, Message: "service unavailable", Cause: cause}
}

func containsProtectedRoute(routes []Route) bool {
	for _, route := range routes {
		if route.Access == AccessProtected {
			return true
		}
	}
	return false
}

func validateStaticUpstreams(routes []Route, resolver UpstreamResolver) error {
	for _, route := range routes {
		compiled, err := compileRoute(route)
		if err != nil {
			return err
		}
		if _, err := resolver.ResolveUpstream(compiled.route); err != nil {
			return errors.New("invalid upstream for route " + route.Name + ": " + err.Error())
		}
	}
	return nil
}

func resolveRemoteAddr(r *http.Request) (string, error) {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil {
		return host, nil
	}
	if net.ParseIP(strings.TrimSpace(r.RemoteAddr)) != nil {
		return strings.TrimSpace(r.RemoteAddr), nil
	}
	return "", errors.New("invalid remote address")
}

func normalizeRequestIDConfig(config RequestIDConfig) (RequestIDConfig, error) {
	config.Header = strings.TrimSpace(config.Header)
	if config.Header == "" {
		config.Header = sharedheaders.HeaderXRequestID
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
	value := strings.TrimSpace(r.Header.Get(gateway.requestID.Header))
	if isSafeRequestID(value, gateway.requestID.MaxLength) {
		return value, nil
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

func stripAndInjectIdentity(header http.Header, identity *Identity) {
	header.Del(sharedheaders.HeaderXUserID)
	header.Del(sharedheaders.HeaderXUserRoles)
	if identity == nil {
		return
	}
	header.Set(sharedheaders.HeaderXUserID, identity.UserID)
	if len(identity.Roles) > 0 {
		header.Set(sharedheaders.HeaderXUserRoles, strings.Join(identity.Roles, ","))
	}
}

func cloneIdentity(identity Identity) Identity {
	identity.Roles = append([]string(nil), identity.Roles...)
	if identity.Attributes != nil {
		attributes := make(map[string]any, len(identity.Attributes))
		for key, value := range identity.Attributes {
			attributes[key] = value
		}
		identity.Attributes = attributes
	}
	return identity
}

// RequestIDFromContext 返回 SDK 写入请求上下文的请求 ID。
func RequestIDFromContext(ctx context.Context) (string, bool) {
	requestID, ok := ctx.Value(requestIDContextKey).(string)
	return requestID, ok
}

// RouteFromContext 返回 SDK 选中的路由副本。
func RouteFromContext(ctx context.Context) (Route, bool) {
	route, ok := ctx.Value(routeContextKey).(Route)
	return cloneRoute(route), ok
}

// IdentityFromContext 返回 SDK 认证后的身份副本。
func IdentityFromContext(ctx context.Context) (Identity, bool) {
	identity, ok := ctx.Value(identityContextKey).(Identity)
	return cloneIdentity(identity), ok
}

// ClientIPFromContext 返回经过可信代理策略解析的客户端地址。
func ClientIPFromContext(ctx context.Context) (string, bool) {
	clientIP, ok := ctx.Value(clientIPContextKey).(string)
	return clientIP, ok
}

type defaultErrorResponder struct{}

func (defaultErrorResponder) Respond(w http.ResponseWriter, _ *http.Request, gatewayError GatewayError) {
	if gatewayError.RetryAfter > 0 {
		seconds := int64((gatewayError.RetryAfter + 999999999) / 1000000000)
		w.Header().Set("Retry-After", itoa(int(seconds)))
	}
	sharedapi.WriteError(w, gatewayError.Status, gatewayError.Message)
}
