package gateway

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
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
	if response.Body.String() != "service unavailable\n" {
		t.Fatalf("body = %q", response.Body.String())
	}
}

func TestGatewayRejectsInvalidDynamicRouteBeforeProxying(t *testing.T) {
	proxied := false
	gateway, err := New(
		WithRouteResolver(RouteResolverFunc(func(*http.Request) (Route, bool, error) {
			return Route{Upstream: "backend", Access: AccessPublic}, true, nil
		})),
		withTestUpstreams(map[string]http.Handler{"backend": http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			proxied = true
		})}),
	)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	gateway.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://gateway/dynamic", nil))
	if response.Code != http.StatusServiceUnavailable || proxied {
		t.Fatalf("status = %d, proxied = %v", response.Code, proxied)
	}
}

func TestComponentPanicReturns500AndProducesAccessLog(t *testing.T) {
	accessLogs := make([]AccessLog, 0, 1)
	gateway, err := New(
		WithRouteResolver(RouteResolverFunc(func(*http.Request) (Route, bool, error) {
			panic("route resolver failed")
		})),
		WithAccessLogger(AccessLoggerFunc(func(_ context.Context, accessLog AccessLog) error {
			accessLogs = append(accessLogs, accessLog)
			return nil
		})),
		withTestUpstreams(map[string]http.Handler{"backend": http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})}),
	)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	gateway.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://gateway/public", nil))
	if response.Code != http.StatusInternalServerError || len(accessLogs) != 1 {
		t.Fatalf("status = %d, access logs = %d", response.Code, len(accessLogs))
	}
	if accessLogs[0].ErrorCode != "gateway_panic" {
		t.Fatalf("access log = %#v", accessLogs[0])
	}
}

func TestChunkedRequestAboveRouteLimitReturns413(t *testing.T) {
	route := publicRoute()
	route.MaxBodyBytes = 4
	gateway, err := New(
		WithRoutes(route),
		WithUpstreams(Upstream{Name: "backend", URL: "http://backend.internal"}),
		WithTransport(roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			_, readErr := io.ReadAll(r.Body)
			return nil, readErr
		})),
	)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "http://gateway/public", strings.NewReader("too large"))
	request.ContentLength = -1
	response := httptest.NewRecorder()
	gateway.ServeHTTP(response, request)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
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
