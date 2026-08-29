package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDefaultHealthResponseIsProtocolNeutral(t *testing.T) {
	handler, err := New(
		WithRoutes(publicRoute()),
		withTestUpstreams(map[string]http.Handler{"backend": http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})}),
		WithHealth(HealthConfig{Service: "example-gateway"}),
	)
	if err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{"/health/live", "/health/ready"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://gateway"+path, nil))
		if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "text/plain; charset=utf-8" {
			t.Fatalf("path = %s, status = %d, content type = %q", path, response.Code, response.Header().Get("Content-Type"))
		}
		if response.Body.String() != "ok\n" {
			t.Fatalf("path = %s, body = %q", path, response.Body.String())
		}
	}
}

func TestDefaultErrorResponseIsProtocolNeutralAndDoesNotLeakCause(t *testing.T) {
	handler, err := New(
		WithRoutes(publicRoute()),
		WithClientIPRateLimiter(RateLimiterFunc(func(context.Context, RateLimitRequest) (RateLimitDecision, error) {
			return RateLimitDecision{}, errors.New("redis endpoint contains secret")
		})),
		withTestUpstreams(map[string]http.Handler{"backend": http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})}),
	)
	if err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://gateway/public", nil))
	if response.Code != http.StatusServiceUnavailable || response.Header().Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Fatalf("status = %d, content type = %q", response.Code, response.Header().Get("Content-Type"))
	}
	if response.Body.String() != "service unavailable\n" || strings.Contains(response.Body.String(), "secret") {
		t.Fatalf("body = %q", response.Body.String())
	}
}

func TestCustomHealthResponderOwnsSuccessfulRepresentation(t *testing.T) {
	results := make([]HealthResult, 0, 2)
	handler, err := New(
		WithRoutes(publicRoute()),
		withTestUpstreams(map[string]http.Handler{"backend": http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})}),
		WithHealth(HealthConfig{
			Service: "  example-gateway  ",
			Responder: HealthResponderFunc(func(w http.ResponseWriter, _ *http.Request, result HealthResult) {
				results = append(results, result)
				w.Header().Set("Content-Type", "application/vnd.example.health+json")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]string{"kind": string(result.Kind), "service": result.Service})
			}),
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{"/health/live", "/health/ready"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://gateway"+path, nil))
		if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "application/vnd.example.health+json" {
			t.Fatalf("path = %s, status = %d, content type = %q", path, response.Code, response.Header().Get("Content-Type"))
		}
	}
	if len(results) != 2 || results[0] != (HealthResult{Kind: HealthKindLive, Service: "example-gateway"}) || results[1] != (HealthResult{Kind: HealthKindReady, Service: "example-gateway"}) {
		t.Fatalf("results = %#v", results)
	}
}

func TestReadinessFailureUsesErrorResponder(t *testing.T) {
	healthResponses := 0
	var received GatewayError
	handler, err := New(
		WithRoutes(publicRoute()),
		withTestUpstreams(map[string]http.Handler{"backend": http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})}),
		WithErrorResponder(ErrorResponderFunc(func(w http.ResponseWriter, _ *http.Request, gatewayError GatewayError) {
			received = gatewayError
			w.WriteHeader(gatewayError.Status)
		})),
		WithHealth(HealthConfig{
			Readiness: ReadinessCheckerFunc(func(context.Context) error { return errors.New("dependency unavailable") }),
			Responder: HealthResponderFunc(func(http.ResponseWriter, *http.Request, HealthResult) { healthResponses++ }),
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://gateway/health/ready", nil))
	if response.Code != http.StatusServiceUnavailable || healthResponses != 0 {
		t.Fatalf("status = %d, health responses = %d", response.Code, healthResponses)
	}
	if received.Code != "readiness_failed" || received.Cause == nil {
		t.Fatalf("gateway error = %#v", received)
	}
}

func TestCustomErrorResponderReceivesSemanticsAfterProtocolHeaders(t *testing.T) {
	var received GatewayError
	var retryAfter string
	handler := mustGateway(t,
		WithClientIPRateLimiter(RateLimiterFunc(func(context.Context, RateLimitRequest) (RateLimitDecision, error) {
			return RateLimitDecision{Allowed: false, RetryAfter: 1500 * time.Millisecond}, nil
		})),
		WithErrorResponder(ErrorResponderFunc(func(w http.ResponseWriter, _ *http.Request, gatewayError GatewayError) {
			received = gatewayError
			retryAfter = w.Header().Get("Retry-After")
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(gatewayError.Status)
			_ = json.NewEncoder(w).Encode(map[string]string{"type": gatewayError.Code})
		})),
	)

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://gateway/public", nil))
	if response.Code != http.StatusTooManyRequests || received.Code != "rate_limit_exceeded" || received.RetryAfter != 1500*time.Millisecond {
		t.Fatalf("status = %d, gateway error = %#v", response.Code, received)
	}
	if retryAfter != "2" || response.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf("Retry-After = %q, content type = %q", retryAfter, response.Header().Get("Content-Type"))
	}
}

func TestReverseProxyUsesConfiguredErrorResponder(t *testing.T) {
	var received GatewayError
	handler, err := New(
		WithRoutes(publicRoute()),
		WithUpstreams(Upstream{Name: "backend", URL: "http://backend.invalid"}),
		WithTransport(roundTripperFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("upstream transport failed")
		})),
		WithErrorResponder(ErrorResponderFunc(func(w http.ResponseWriter, _ *http.Request, gatewayError GatewayError) {
			received = gatewayError
			w.WriteHeader(gatewayError.Status)
		})),
	)
	if err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://gateway/public", nil))
	if response.Code != http.StatusBadGateway || received.Code != "upstream_unavailable" || received.Cause == nil {
		t.Fatalf("status = %d, gateway error = %#v", response.Code, received)
	}
}

func TestResponderPanicUsesSafeFallback(t *testing.T) {
	tests := map[string]Option{
		"error": WithErrorResponder(ErrorResponderFunc(func(http.ResponseWriter, *http.Request, GatewayError) {
			panic("error responder failed")
		})),
		"health": WithHealth(HealthConfig{Responder: HealthResponderFunc(func(http.ResponseWriter, *http.Request, HealthResult) {
			panic("health responder failed")
		})}),
	}
	for name, option := range tests {
		t.Run(name, func(t *testing.T) {
			handler := mustGateway(t, option)
			path := "/missing"
			if name == "health" {
				path = "/health/live"
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://gateway"+path, nil))
			if response.Code != http.StatusInternalServerError || response.Body.String() != "internal server error\n" {
				t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
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
