package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCORSPreflightUsesExplicitHeadersWithoutProxying(t *testing.T) {
	proxied := false
	gateway, err := New(
		WithRoutes(publicRoute()),
		WithCORS(CORSConfig{
			AllowedOrigins: []string{"https://app.example.com"},
			AllowedMethods: []string{http.MethodGet},
			AllowedHeaders: []string{"Authorization", "X-Request-ID"},
			MaxAge:         time.Minute,
		}),
		withTestUpstreams(map[string]http.Handler{"backend": http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			proxied = true
		})}),
	)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodOptions, "http://gateway/public", nil)
	request.Header.Set("Origin", "https://app.example.com")
	request.Header.Set("Access-Control-Request-Method", http.MethodGet)
	request.Header.Set("Access-Control-Request-Headers", "Authorization")
	response := httptest.NewRecorder()
	gateway.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || proxied {
		t.Fatalf("status = %d, proxied = %v", response.Code, proxied)
	}
	if got := response.Header().Get("Access-Control-Allow-Headers"); got != "Authorization,X-Request-Id" {
		t.Fatalf("allowed headers = %q", got)
	}
}

func TestCORSRejectsUnknownRequestedHeader(t *testing.T) {
	gateway, err := New(
		WithRoutes(publicRoute()),
		WithCORS(CORSConfig{AllowedOrigins: []string{"https://app.example.com"}}),
		withTestUpstreams(map[string]http.Handler{"backend": http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Fatal("request reached upstream")
		})}),
	)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodOptions, "http://gateway/public", nil)
	request.Header.Set("Origin", "https://app.example.com")
	request.Header.Set("Access-Control-Request-Method", http.MethodGet)
	request.Header.Set("Access-Control-Request-Headers", "X-Not-Allowed")
	response := httptest.NewRecorder()
	gateway.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestCORSRejectsWildcardWithCredentials(t *testing.T) {
	_, err := New(
		WithRoutes(publicRoute()),
		WithUpstreams(Upstream{Name: "backend", URL: "http://backend.internal"}),
		WithCORS(CORSConfig{AllowedOrigins: []string{"*"}, AllowCredentials: true}),
	)
	if err == nil || !strings.Contains(err.Error(), "wildcard") {
		t.Fatalf("error = %v", err)
	}
}
