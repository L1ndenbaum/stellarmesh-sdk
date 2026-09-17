package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

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
