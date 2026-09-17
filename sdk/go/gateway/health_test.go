package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHealthEndpointsBypassRoutesAndSkipSuccessfulAccessLog(t *testing.T) {
	accessLogs := make([]AccessLog, 0, 2)
	proxied := false
	gateway, err := New(
		WithRoutes(publicRoute()),
		WithHealth(HealthConfig{Service: "edge-gateway"}),
		WithAccessLogger(AccessLoggerFunc(func(_ context.Context, accessLog AccessLog) error {
			accessLogs = append(accessLogs, accessLog)
			return nil
		})),
		withTestUpstreams(map[string]http.Handler{"backend": http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			proxied = true
		})}),
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/health/live", "/health/ready"} {
		response := httptest.NewRecorder()
		gateway.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://gateway"+path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("%s status = %d, body = %s", path, response.Code, response.Body.String())
		}
	}
	if proxied || len(accessLogs) != 0 {
		t.Fatalf("proxied = %v, access logs = %d", proxied, len(accessLogs))
	}
}

func TestReadinessFailureReturns503AndIsObserved(t *testing.T) {
	accessLogs := make([]AccessLog, 0, 1)
	observations := make([]Observation, 0, 2)
	gateway, err := New(
		WithRoutes(publicRoute()),
		WithHealth(HealthConfig{
			Readiness: ReadinessCheckerFunc(func(context.Context) error { return errors.New("dependency unavailable") }),
		}),
		WithAccessLogger(AccessLoggerFunc(func(_ context.Context, accessLog AccessLog) error {
			accessLogs = append(accessLogs, accessLog)
			return nil
		})),
		WithObserver(ObserverFunc(func(_ context.Context, observation Observation) {
			observations = append(observations, observation)
		})),
		withTestUpstreams(map[string]http.Handler{"backend": http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})}),
	)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	gateway.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://gateway/health/ready", nil))
	if response.Code != http.StatusServiceUnavailable || len(accessLogs) != 1 {
		t.Fatalf("status = %d, access logs = %d", response.Code, len(accessLogs))
	}
	if got := accessLogs[0].ErrorCode; got != "readiness_failed" {
		t.Fatalf("error_code = %#v", got)
	}
	if len(observations) != 2 || observations[0].Kind != ObservationComponentFailure || observations[0].Component != "readiness_failed" {
		t.Fatalf("observations = %#v", observations)
	}
}

func TestReadinessTimeoutFailsClosed(t *testing.T) {
	gateway, err := New(
		WithRoutes(publicRoute()),
		WithHealth(HealthConfig{
			CheckTimeout: time.Millisecond,
			Readiness: ReadinessCheckerFunc(func(ctx context.Context) error {
				<-ctx.Done()
				return ctx.Err()
			}),
		}),
		withTestUpstreams(map[string]http.Handler{"backend": http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})}),
	)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	gateway.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://gateway/health/ready", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", response.Code)
	}
}

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
