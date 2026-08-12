package gateway

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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

func TestReverseProxyRebuildsForwardingHeaders(t *testing.T) {
	upstreamRequests := make(chan *http.Request, 2)
	gateway, err := New(
		WithRoutes(publicRoute()),
		WithUpstreams(Upstream{Name: "backend", URL: "http://backend.internal"}),
		WithTransport(roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			clone := r.Clone(r.Context())
			clone.Header = r.Header.Clone()
			upstreamRequests <- clone
			return &http.Response{
				StatusCode: http.StatusNoContent,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("")),
				Request:    r,
			}, nil
		})),
	)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		request := httptest.NewRequest(http.MethodGet, "http://gateway/public", nil)
		request.RemoteAddr = "203.0.113.10:4321"
		request.Header.Set("X-Forwarded-For", "198.51.100.99")
		response := httptest.NewRecorder()
		gateway.ServeHTTP(response, request)
		if response.Code != http.StatusNoContent {
			t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
		}
	}
	for range 2 {
		request := <-upstreamRequests
		if got := request.Header.Get("X-Forwarded-For"); got != "203.0.113.10" {
			t.Fatalf("X-Forwarded-For = %q", got)
		}
	}
}

func TestReverseProxyReportsTransportTimeoutAsGatewayTimeout(t *testing.T) {
	gateway, err := New(
		WithRoutes(publicRoute()),
		WithUpstreams(Upstream{Name: "backend", URL: "http://backend.invalid"}),
		WithTransport(roundTripperFunc(func(*http.Request) (*http.Response, error) {
			return nil, timeoutError{}
		})),
	)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	gateway.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://gateway/public", nil))
	if response.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestReverseProxyFlushesEventStreamThroughRecorder(t *testing.T) {
	gateway, err := New(
		WithRoutes(publicRoute()),
		WithUpstreams(Upstream{Name: "backend", URL: "http://backend.internal"}),
		WithTransport(roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader("data: ready\n\n")),
				Request:    r,
			}, nil
		})),
	)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	gateway.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://gateway/public", nil))
	if response.Code != http.StatusOK || response.Body.String() != "data: ready\n\n" || !response.Flushed {
		t.Fatalf("status = %d, body = %q, flushed = %v", response.Code, response.Body.String(), response.Flushed)
	}
}

func TestGatewayValidatesUpstreamsAtConstruction(t *testing.T) {
	_, err := New(
		WithRoutes(Route{Name: "public", Match: RouteMatch{ExactPath: "/public"}, Upstream: "missing", Access: AccessPublic}),
		WithUpstreams(Upstream{Name: "backend", URL: "http://backend.internal"}),
	)
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("error = %v", err)
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

func TestResponseRecorderPreservesResponseControllerCapabilities(t *testing.T) {
	underlying := &controllerWriter{header: make(http.Header)}
	recorder := newResponseRecorder(underlying)
	controller := http.NewResponseController(recorder)
	if err := controller.Flush(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := controller.Hijack(); err != nil {
		t.Fatal(err)
	}
	if !underlying.flushed || !underlying.hijacked {
		t.Fatalf("flushed = %v, hijacked = %v", underlying.flushed, underlying.hijacked)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (roundTrip roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

type timeoutError struct{}

func (timeoutError) Error() string   { return "timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

type controllerWriter struct {
	header   http.Header
	flushed  bool
	hijacked bool
}

func (writer *controllerWriter) Header() http.Header            { return writer.header }
func (writer *controllerWriter) WriteHeader(int)                {}
func (writer *controllerWriter) Write(body []byte) (int, error) { return len(body), nil }
func (writer *controllerWriter) Flush()                         { writer.flushed = true }
func (writer *controllerWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	writer.hijacked = true
	return nil, nil, nil
}
