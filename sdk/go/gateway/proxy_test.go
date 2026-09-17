package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (roundTrip roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

type timeoutError struct{}

func (timeoutError) Error() string { return "timeout" }

func (timeoutError) Timeout() bool { return true }

func (timeoutError) Temporary() bool { return true }
