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
