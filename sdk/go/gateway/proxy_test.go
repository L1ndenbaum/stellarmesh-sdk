package gateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func TestReverseProxyPreservesSelectedRequestID(t *testing.T) {
	for _, config := range []struct {
		name          string
		header        string
		connection    bool
		trustIncoming bool
	}{
		{name: "default"},
		{name: "default_connection", connection: true},
		{name: "custom", header: "X-Correlation-ID"},
		{name: "custom_connection", header: "X-Correlation-ID", connection: true},
		{name: "trusted_connection", connection: true, trustIncoming: true},
		{name: "trusted_custom_connection", header: "X-Correlation-ID", connection: true, trustIncoming: true},
	} {
		for _, upstreamResponse := range []string{"absent", "echo", "different", "multiple"} {
			t.Run(config.name+"/"+upstreamResponse, func(t *testing.T) {
				header := config.header
				if header == "" {
					header = "X-Request-ID"
				}
				want, wantGenerated := "gateway-id", 1
				if config.trustIncoming {
					want, wantGenerated = "incoming-id", 0
				}
				forwarded := make(chan []string, 1)
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					forwarded <- slices.Clone(r.Header.Values(header))
					switch upstreamResponse {
					case "echo":
						w.Header().Set(header, r.Header.Get(header))
					case "different":
						w.Header().Set(header, "backend-id")
					case "multiple":
						w.Header().Add(header, "backend-id-1")
						w.Header().Add(header, "backend-id-2")
					}
					w.Header().Set("X-Upstream-Result", "retained")
					w.WriteHeader(http.StatusNoContent)
				}))
				defer upstream.Close()
				transport := http.DefaultTransport.(*http.Transport).Clone()
				defer transport.CloseIdleConnections()
				var contextID, policyID, loggedID string
				generated := 0
				handler, err := New(
					WithRoutes(publicRoute()),
					WithUpstreams(Upstream{Name: "backend", URL: upstream.URL}),
					WithTransport(transport),
					WithRequestID(RequestIDConfig{
						Header: config.header, TrustIncoming: config.trustIncoming,
						Generate: func() (string, error) { generated++; return "gateway-id", nil },
					}),
					WithBeforeProxyPolicy(BeforeProxyPolicyFunc(func(ctx context.Context, _ *http.Request, request RequestContext) (PolicyDecision, error) {
						contextID, _ = RequestIDFromContext(ctx)
						policyID = request.RequestID
						return PolicyDecision{Allowed: true}, nil
					})),
					WithAccessLogger(AccessLoggerFunc(func(_ context.Context, entry AccessLog) error {
						loggedID = entry.RequestID
						return nil
					})),
				)
				if err != nil {
					t.Fatal(err)
				}
				request := httptest.NewRequest(http.MethodGet, "http://gateway/public", nil)
				request.Header.Set(header, "incoming-id")
				if config.connection {
					request.Header.Set("Connection", "keep-alive, "+strings.ToLower(header))
				}
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				if response.Code != http.StatusNoContent {
					t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
				}
				if got := <-forwarded; len(got) != 1 || got[0] != want {
					t.Errorf("forwarded request ID = %v, want [%s]", got, want)
				}
				result := response.Result()
				defer result.Body.Close()
				if got := result.Header.Values(header); len(got) != 1 || got[0] != want {
					t.Errorf("response request ID = %v, want [%s]", got, want)
				}
				if contextID != want || policyID != want || loggedID != want {
					t.Errorf("context = %q, policy = %q, log = %q, want %q", contextID, policyID, loggedID, want)
				}
				if generated != wantGenerated {
					t.Errorf("generator calls = %d, want %d", generated, wantGenerated)
				}
				if got := result.Header.Get("X-Upstream-Result"); got != "retained" {
					t.Errorf("unrelated upstream header = %q", got)
				}
			})
		}
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

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (roundTrip roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

type timeoutError struct{}

func (timeoutError) Error() string { return "timeout" }

func (timeoutError) Timeout() bool { return true }

func (timeoutError) Temporary() bool { return true }
