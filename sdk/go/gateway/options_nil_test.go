package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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

func TestGatewayRejectsTypedNilResolvedUpstream(t *testing.T) {
	var upstream *typedNilHandler
	handler, err := New(
		WithRoutes(publicRoute()),
		WithUpstreamResolver(UpstreamResolverFunc(func(Route) (http.Handler, error) {
			return upstream, nil
		})),
		WithoutAccessLog(),
	)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://gateway/public", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}
}

type typedNilHandler struct{}

func (*typedNilHandler) ServeHTTP(http.ResponseWriter, *http.Request) {}
