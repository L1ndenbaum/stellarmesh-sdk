package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTrustedProxyResolverRejectsSpoofedForwardedHeaderFromPublicClient(t *testing.T) {
	gateway, err := New(
		WithRoutes(publicRoute()),
		WithTrustedProxies("10.0.0.0/8"),
		WithClientIPRateLimiter(RateLimiterFunc(func(_ context.Context, request RateLimitRequest) (RateLimitDecision, error) {
			if request.Key != "203.0.113.9" {
				t.Fatalf("client IP = %q", request.Key)
			}
			return RateLimitDecision{Allowed: true}, nil
		})),
		withTestUpstreams(map[string]http.Handler{"backend": http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})}),
	)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "http://gateway/public", nil)
	request.RemoteAddr = "203.0.113.9:1234"
	request.Header.Set("X-Forwarded-For", "198.51.100.7")
	response := httptest.NewRecorder()
	gateway.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestTrustedProxyResolverWalksForwardedChainFromRight(t *testing.T) {
	config := config{configuredComponents: make(map[string]struct{})}
	if err := WithTrustedProxies("10.0.0.0/8").apply(&config); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "http://gateway", nil)
	request.RemoteAddr = "10.0.0.3:8080"
	request.Header.Set("X-Forwarded-For", "198.51.100.8, 10.0.0.2")
	clientIP, err := config.clientIPResolver.Resolve(request)
	if err != nil || clientIP != "198.51.100.8" {
		t.Fatalf("Resolve() = %q, %v", clientIP, err)
	}
}

func TestTrustedProxyResolverFailsClosedOnMalformedForwardedChain(t *testing.T) {
	gateway, err := New(
		WithRoutes(publicRoute()),
		WithTrustedProxies("10.0.0.0/8"),
		withTestUpstreams(map[string]http.Handler{"backend": http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Fatal("request reached upstream")
		})}),
	)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "http://gateway/public", nil)
	request.RemoteAddr = "10.0.0.3:8080"
	request.Header.Set("X-Forwarded-For", "not-an-ip")
	response := httptest.NewRecorder()
	gateway.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", response.Code)
	}
}
