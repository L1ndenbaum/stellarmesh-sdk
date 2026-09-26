package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
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

func TestRequestIDDefaultIgnoresIncoming(t *testing.T) {
	handler := mustGateway(t, WithoutAccessLog())
	request := httptest.NewRequest(http.MethodGet, "http://gateway/public", nil)
	request.Header.Set("X-Request-ID", "client-id")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if got := response.Header().Get("X-Request-ID"); got == "" || got == "client-id" {
		t.Fatalf("default request ID = %q", got)
	}
}

func TestRequestIDTrustIncoming(t *testing.T) {
	for _, test := range []struct {
		name   string
		trust  bool
		header string
		values []string
		want   string
	}{
		{name: "disabled", values: []string{"client-id"}, want: "generated-id"},
		{name: "disabled_custom_header", header: "X-Correlation-ID", values: []string{"client-id"}, want: "generated-id"},
		{name: "valid", trust: true, values: []string{"client-id"}, want: "client-id"},
		{name: "custom_header", trust: true, header: "X-Correlation-ID", values: []string{"client-id"}, want: "client-id"},
		{name: "trimmed", trust: true, values: []string{"  client-id\t"}, want: "client-id"},
		{name: "missing", trust: true, want: "generated-id"},
		{name: "empty", trust: true, values: []string{" "}, want: "generated-id"},
		{name: "too_long", trust: true, values: []string{strings.Repeat("x", 129)}, want: "generated-id"},
		{name: "invalid", trust: true, values: []string{"client\x00id"}, want: "generated-id"},
		{name: "duplicate", trust: true, values: []string{"first", "second"}, want: "generated-id"},
		{name: "duplicate_same_value", trust: true, values: []string{"client-id", "client-id"}, want: "generated-id"},
	} {
		t.Run(test.name, func(t *testing.T) {
			header := test.header
			if header == "" {
				header = "X-Request-ID"
			}
			generated, forwarded := 0, false
			var logged AccessLog
			handler, err := New(
				WithRoutes(publicRoute()),
				WithRequestID(RequestIDConfig{
					Header: test.header, TrustIncoming: test.trust,
					Generate: func() (string, error) { generated++; return "generated-id", nil },
				}),
				// 可信代理只影响客户端 IP，不应隐式开启请求 ID 信任。
				WithTrustedProxies("192.0.2.0/24"),
				WithAccessLogger(AccessLoggerFunc(func(_ context.Context, entry AccessLog) error {
					logged = entry
					return nil
				})),
				withTestUpstreams(map[string]http.Handler{"backend": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					forwarded = true
					if got := r.Header.Values(header); !slices.Equal(got, []string{test.want}) {
						t.Errorf("forwarded request ID = %v", got)
					}
					if got, ok := RequestIDFromContext(r.Context()); !ok || got != test.want {
						t.Errorf("context request ID = %q, %v", got, ok)
					}
					w.WriteHeader(http.StatusNoContent)
				})}),
			)
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodGet, "http://gateway/public", nil)
			for _, value := range test.values {
				request.Header.Add(header, value)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusNoContent || !forwarded {
				t.Fatalf("status = %d, forwarded = %v", response.Code, forwarded)
			}
			if got := response.Header().Values(header); !slices.Equal(got, []string{test.want}) || logged.RequestID != test.want {
				t.Fatalf("response ID = %v, logged ID = %q", got, logged.RequestID)
			}
			wantGenerated := 0
			if test.want == "generated-id" {
				wantGenerated = 1
			}
			if generated != wantGenerated {
				t.Fatalf("generator calls = %d, want %d", generated, wantGenerated)
			}
		})
	}
}

func TestRequestIDGenerationFailureDoesNotTrustIncoming(t *testing.T) {
	failure := errors.New("random source unavailable")
	for _, test := range []struct {
		name  string
		value string
		err   error
	}{
		{name: "error", err: failure},
		{name: "empty"},
		{name: "invalid", value: "bad id"},
		{name: "too_long", value: strings.Repeat("x", 129)},
	} {
		t.Run(test.name, func(t *testing.T) {
			var reported GatewayError
			handler, err := New(
				WithRoutes(publicRoute()),
				WithRequestID(RequestIDConfig{Generate: func() (string, error) { return test.value, test.err }}),
				WithoutAccessLog(),
				WithErrorResponder(ErrorResponderFunc(func(w http.ResponseWriter, r *http.Request, gatewayError GatewayError) {
					reported = gatewayError
					if got := r.Header.Get("X-Request-ID"); got != "" {
						t.Errorf("untrusted ID reached error responder: %q", got)
					}
					w.WriteHeader(gatewayError.Status)
				})),
				withTestUpstreams(map[string]http.Handler{"backend": http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
					t.Error("request forwarded after generation failure")
				})}),
			)
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodGet, "http://gateway/public", nil)
			request.Header.Set("X-Request-ID", "client-id")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusServiceUnavailable || reported.Code != "request_id_unavailable" {
				t.Fatalf("status = %d, error = %+v", response.Code, reported)
			}
			if reported.Cause == nil || (test.err != nil && !errors.Is(reported.Cause, test.err)) {
				t.Fatalf("cause = %v", reported.Cause)
			}
			if got := response.Header().Get("X-Request-ID"); got != "" {
				t.Fatalf("failed request returned ID %q", got)
			}
		})
	}
}
