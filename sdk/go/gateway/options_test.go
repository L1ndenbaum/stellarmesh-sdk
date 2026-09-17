package gateway

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestGatewayRejectsInvalidHTTPConfiguration(t *testing.T) {
	tests := []Option{
		WithRequestID(RequestIDConfig{Header: "X-Bad\nHeader"}),
		WithCORS(CORSConfig{AllowedOrigins: []string{"not-an-origin"}}),
		WithCORS(CORSConfig{AllowedOrigins: []string{"https://app.example.com"}, AllowedHeaders: []string{"X-Bad\nHeader"}}),
	}
	for _, invalid := range tests {
		_, err := New(
			WithRoutes(publicRoute()),
			WithUpstreams(Upstream{Name: "backend", URL: "http://backend.internal"}),
			invalid,
		)
		if err == nil {
			t.Fatalf("New() accepted invalid option %#v", invalid)
		}
	}
}

func TestAccessLogOptionsAreMutuallyExclusive(t *testing.T) {
	custom := AccessLoggerFunc(func(context.Context, AccessLog) error { return nil })
	testCases := [][]Option{
		{WithAccessLogger(custom), WithSlogAccessLogger(SlogAccessLoggerConfig{})},
		{WithSlogAccessLogger(SlogAccessLoggerConfig{}), WithoutAccessLog()},
		{WithoutAccessLog(), WithAccessLogger(custom)},
	}
	for index, options := range testCases {
		if _, err := New(options...); err == nil || !strings.Contains(err.Error(), "duplicate gateway component: access_logger") {
			t.Fatalf("case %d error = %v", index, err)
		}
	}
}

func TestGatewayRejectsTypedNilComponentsAtConstruction(t *testing.T) {
	tests := map[string]Option{
		"option":              optionFunc(nil),
		"route resolver":      WithRouteResolver(RouteResolverFunc(nil)),
		"upstream resolver":   WithUpstreamResolver(UpstreamResolverFunc(nil)),
		"transport":           WithTransport(roundTripperFunc(nil)),
		"authenticator":       WithAuthenticator(AuthenticatorFunc(nil)),
		"authorizer":          WithAuthorizer(AuthorizerFunc(nil)),
		"before proxy policy": WithBeforeProxyPolicy(BeforeProxyPolicyFunc(nil)),
		"client IP resolver":  WithClientIPResolver(ClientIPResolverFunc(nil)),
		"client rate limiter": WithClientIPRateLimiter(RateLimiterFunc(nil)),
		"user rate limiter":   WithUserRateLimiter(RateLimiterFunc(nil)),
		"upstream limiter":    WithUpstreamRateLimiter(RateLimiterFunc(nil)),
		"access logger":       WithAccessLogger(AccessLoggerFunc(nil)),
		"observer":            WithObserver(ObserverFunc(nil)),
	}
	for name, option := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := New(option); err == nil || !strings.Contains(err.Error(), "nil") {
				t.Fatalf("New() error = %v", err)
			}
		})
	}
}

func TestGatewayRejectsNilResponseComponents(t *testing.T) {
	tests := map[string]Option{
		"error nil":        WithErrorResponder(nil),
		"error typed nil":  WithErrorResponder(ErrorResponderFunc(nil)),
		"health typed nil": WithHealth(HealthConfig{Responder: HealthResponderFunc(nil)}),
	}
	for name, option := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := New(
				WithRoutes(publicRoute()),
				withTestUpstreams(map[string]http.Handler{"backend": http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})}),
				option,
			)
			if err == nil || !strings.Contains(err.Error(), "responder is nil") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestGatewayRejectsDuplicateOptions(t *testing.T) {
	_, err := New(
		WithRoutes(publicRoute()),
		withTestUpstreams(map[string]http.Handler{"backend": http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})}),
		WithAuthorizer(AuthorizerFunc(func(context.Context, *http.Request, RequestContext) (PolicyDecision, error) {
			return PolicyDecision{Allowed: true}, nil
		})),
		WithAuthorizer(AuthorizerFunc(func(context.Context, *http.Request, RequestContext) (PolicyDecision, error) {
			return PolicyDecision{Allowed: true}, nil
		})),
	)
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("error = %v", err)
	}
}
