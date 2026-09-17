package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestIDReplacesUnsafeInboundValue(t *testing.T) {
	gateway, err := New(
		WithRoutes(publicRoute()),
		WithRequestID(RequestIDConfig{Generate: func() (string, error) { return "safe-id", nil }}),
		withTestUpstreams(map[string]http.Handler{"backend": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("X-Request-ID"); got != "safe-id" {
				t.Fatalf("request ID = %q", got)
			}
			w.WriteHeader(http.StatusNoContent)
		})}),
	)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "http://gateway/public", nil)
	request.Header["X-Request-Id"] = []string{strings.Repeat("x", 129)}
	response := httptest.NewRecorder()
	gateway.ServeHTTP(response, request)
	if response.Header().Get("X-Request-ID") != "safe-id" {
		t.Fatalf("response request ID = %q", response.Header().Get("X-Request-ID"))
	}
}
