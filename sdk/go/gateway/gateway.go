package gateway

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
)

// Gateway 按固定的安全顺序执行项目声明的组件。
type Gateway struct {
	routes              RouteResolver
	upstreams           UpstreamResolver
	authenticator       Authenticator
	credentialExtractor CredentialExtractor
	authorizer          Authorizer
	beforeProxy         BeforeProxyPolicy
	clientIPResolver    ClientIPResolver
	clientIPLimiter     RateLimiter
	userLimiter         RateLimiter
	upstreamLimiter     RateLimiter
	errorResponder      ErrorResponder
	requestID           RequestIDConfig
	cors                *corsPolicy
	accessLogger        AccessLogger
	observer            Observer
	health              *healthPolicy
}

// New 校验所有声明式组件并构造网关处理器。
func New(options ...Option) (*Gateway, error) {
	config := config{configuredComponents: make(map[string]struct{})}
	for index, option := range options {
		if isNilInterface(option) {
			return nil, errors.New("gateway option at index " + strconv.Itoa(index) + " is nil")
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
	config.errorResponder = protocolErrorResponder{next: config.errorResponder}
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
	health, err := newHealthPolicy(config.health)
	if err != nil {
		return nil, err
	}
	if config.accessLogger == nil && !config.accessLogDisabled {
		config.accessLogger = NewSlogAccessLogger(SlogAccessLoggerConfig{})
	}
	if err := validateStaticUpstreams(config.staticRoutes, config.upstreamResolver); err != nil {
		return nil, err
	}
	return &Gateway{
		routes:              config.routeResolver,
		upstreams:           config.upstreamResolver,
		authenticator:       config.authenticator,
		credentialExtractor: config.credentialExtractor,
		authorizer:          config.authorizer,
		beforeProxy:         config.beforeProxy,
		clientIPResolver:    config.clientIPResolver,
		clientIPLimiter:     config.clientIPLimiter,
		userLimiter:         config.userLimiter,
		upstreamLimiter:     config.upstreamLimiter,
		errorResponder:      config.errorResponder,
		requestID:           requestID,
		cors:                cors,
		accessLogger:        config.accessLogger,
		observer:            config.observer,
		health:              health,
	}, nil
}

// ServeHTTP 执行固定流水线；任何转发决策组件错误都会停止请求。
func (gateway *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	recorder := newResponseRecorder(w)
	accessState := newAccessLogState(r)
	r = r.WithContext(context.WithValue(r.Context(), accessLogStateContextKey, accessState))
	defer gateway.finishRequest(r.Context(), recorder, accessState)
	defer gateway.recoverPanic(recorder, r, accessState)

	requestID, err := gateway.resolveRequestID(r)
	if err != nil {
		gateway.fail(recorder, r, unavailableError("request_id_unavailable", err))
		return
	}
	r.Header.Set(gateway.requestID.Header, requestID)
	recorder.Header().Set(gateway.requestID.Header, requestID)
	accessState.RequestID = requestID
	r = r.WithContext(context.WithValue(r.Context(), requestIDContextKey, requestID))
	if gateway.cors != nil && gateway.cors.handle(recorder, r, gateway) {
		return
	}
	if gateway.health != nil && gateway.health.handle(recorder, r, gateway, accessState) {
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
	route, err = normalizeResolvedRoute(route)
	if err != nil {
		gateway.fail(recorder, r, unavailableError("invalid_resolved_route", err))
		return
	}
	accessState.Route = route.Name
	accessState.Upstream = route.Upstream
	r = r.WithContext(context.WithValue(r.Context(), routeContextKey, route))
	upstream, err := gateway.upstreams.ResolveUpstream(route)
	if err != nil || isNilInterface(upstream) {
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
	accessState.ClientIP = clientIP
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

func (gateway *Gateway) recoverPanic(w *responseRecorder, r *http.Request, state *accessLogState) {
	if recovered := recover(); recovered != nil {
		if recovered == http.ErrAbortHandler {
			state.ErrorCode = "upstream_stream_aborted"
			state.FailureComponent = "upstream_stream_aborted"
			return
		}
		state.ErrorCode = "gateway_panic"
		state.FailureComponent = "gateway_panic"
		if !w.WroteHeader() {
			gateway.fail(w, r, GatewayError{Status: http.StatusInternalServerError, Code: "gateway_panic", Message: "internal server error", Cause: errors.New("gateway component panic")})
		}
	}
}
