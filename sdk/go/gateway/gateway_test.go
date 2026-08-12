package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestGatewayRunsSecurityStagesInFixedOrder(t *testing.T) {
	stages := make([]string, 0, 6)
	appendStage := func(name string) { stages = append(stages, name) }
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		appendStage("proxy")
		if got := r.Header.Get("X-User-ID"); got != "user-1" {
			t.Fatalf("X-User-ID = %q", got)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	gateway, err := New(
		WithRoutes(Route{Name: "api", Match: RouteMatch{PathPrefix: "/api/"}, Upstream: "backend"}),
		withTestUpstreams(map[string]http.Handler{"backend": handler}),
		WithAuthenticator(AuthenticatorFunc(func(context.Context, string) (AuthenticationDecision, error) {
			appendStage("authenticate")
			return AuthenticationDecision{Authenticated: true, Identity: Identity{UserID: "user-1"}}, nil
		})),
		WithClientIPRateLimiter(recordingLimiter("client", appendStage)),
		WithUserRateLimiter(recordingLimiter("user", appendStage)),
		WithAuthorizer(AuthorizerFunc(func(context.Context, *http.Request, RequestContext) (PolicyDecision, error) {
			appendStage("authorize")
			return PolicyDecision{Allowed: true}, nil
		})),
		WithBeforeProxyPolicy(BeforeProxyPolicyFunc(func(context.Context, *http.Request, RequestContext) (PolicyDecision, error) {
			appendStage("policy")
			return PolicyDecision{Allowed: true}, nil
		})),
		WithUpstreamRateLimiter(recordingLimiter("upstream", appendStage)),
	)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "http://gateway/api/items", nil)
	request.Header.Set("Authorization", "Bearer token")
	request.Header.Set("X-User-ID", "spoofed")
	response := httptest.NewRecorder()
	gateway.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	want := []string{"client", "authenticate", "user", "authorize", "policy", "upstream", "proxy"}
	if !reflect.DeepEqual(stages, want) {
		t.Fatalf("stages = %#v, want %#v", stages, want)
	}
}

func TestGatewayFailsClosedWhenLimiterFails(t *testing.T) {
	proxied := false
	gateway, err := New(
		WithRoutes(publicRoute()),
		WithClientIPRateLimiter(RateLimiterFunc(func(context.Context, RateLimitRequest) (RateLimitDecision, error) {
			return RateLimitDecision{}, errors.New("redis unavailable")
		})),
		withTestUpstreams(map[string]http.Handler{"backend": http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			proxied = true
		})}),
	)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	gateway.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://gateway/public", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if proxied {
		t.Fatal("request reached upstream after limiter failure")
	}
}

func TestGatewayRejectsProtectedRoutesWithoutAuthenticatorAtConstruction(t *testing.T) {
	_, err := New(
		WithRoutes(Route{Name: "protected", Match: RouteMatch{ExactPath: "/private"}, Upstream: "backend"}),
		withTestUpstreams(map[string]http.Handler{"backend": http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})}),
	)
	if err == nil || !strings.Contains(err.Error(), "authenticator") {
		t.Fatalf("error = %v", err)
	}
}

func TestGatewayDynamicProtectedRouteWithoutAuthenticatorFailsClosed(t *testing.T) {
	gateway, err := New(
		WithRouteResolver(RouteResolverFunc(func(*http.Request) (Route, bool, error) {
			return Route{Name: "dynamic", Upstream: "backend", Access: AccessProtected}, true, nil
		})),
		withTestUpstreams(map[string]http.Handler{"backend": http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Fatal("request reached upstream")
		})}),
	)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "http://gateway/dynamic", nil)
	request.Header.Set("Authorization", "Bearer token")
	gateway.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestStaticRoutesPreferExactThenLongestPrefix(t *testing.T) {
	resolver, err := newStaticRouteResolver([]Route{
		{Name: "root", Match: RouteMatch{PathPrefix: "/api/"}, Upstream: "root", Access: AccessPublic},
		{Name: "nested", Match: RouteMatch{PathPrefix: "/api/v1/"}, Upstream: "nested", Access: AccessPublic},
		{Name: "exact", Match: RouteMatch{ExactPath: "/api/v1/items"}, Upstream: "exact", Access: AccessPublic},
	})
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{
		"/api/v1/items": "exact",
		"/api/v1/other": "nested",
		"/api/v2/items": "root",
	} {
		route, found, resolveErr := resolver.Resolve(httptest.NewRequest(http.MethodGet, path, nil))
		if resolveErr != nil || !found || route.Name != want {
			t.Fatalf("Resolve(%q) = %#v, %v, %v; want %q", path, route, found, resolveErr, want)
		}
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

func TestGatewayReturnsRateLimitRetryAfter(t *testing.T) {
	gateway := mustGateway(t,
		WithClientIPRateLimiter(RateLimiterFunc(func(context.Context, RateLimitRequest) (RateLimitDecision, error) {
			return RateLimitDecision{Allowed: false, RetryAfter: 1500 * time.Millisecond}, nil
		})),
	)
	response := httptest.NewRecorder()
	gateway.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://gateway/public", nil))
	if response.Code != http.StatusTooManyRequests || response.Header().Get("Retry-After") != "2" {
		t.Fatalf("status = %d, Retry-After = %q", response.Code, response.Header().Get("Retry-After"))
	}
}

func recordingLimiter(name string, appendStage func(string)) RateLimiter {
	return RateLimiterFunc(func(context.Context, RateLimitRequest) (RateLimitDecision, error) {
		appendStage(name)
		return RateLimitDecision{Allowed: true}, nil
	})
}

func mustGateway(t *testing.T, options ...Option) *Gateway {
	t.Helper()
	base := []Option{
		WithRoutes(publicRoute()),
		withTestUpstreams(map[string]http.Handler{"backend": http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})}),
	}
	base = append(base, options...)
	gateway, err := New(base...)
	if err != nil {
		t.Fatal(err)
	}
	return gateway
}

func publicRoute() Route {
	return Route{Name: "public", Match: RouteMatch{ExactPath: "/public"}, Upstream: "backend", Access: AccessPublic}
}

func withTestUpstreams(upstreams map[string]http.Handler) Option {
	return WithUpstreamResolver(UpstreamResolverFunc(func(route Route) (http.Handler, error) {
		handler, ok := upstreams[route.Upstream]
		if !ok {
			return nil, errors.New("missing test upstream")
		}
		return handler, nil
	}))
}
