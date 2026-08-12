package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	sharedlogging "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging"
)

func TestAccessLogEmitterOmitsQueryAndCredentials(t *testing.T) {
	emitter := &recordingEmitter{accepted: true}
	gateway, err := New(
		WithRoutes(publicRoute()),
		WithAccessLogEmitter("edge-gateway", emitter),
		withTestUpstreams(map[string]http.Handler{"backend": http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})}),
	)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "http://gateway/public?token=super-secret", nil)
	request.Header.Set("Authorization", "Bearer credential")
	response := httptest.NewRecorder()
	gateway.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || len(emitter.events) != 1 {
		t.Fatalf("status = %d, events = %d", response.Code, len(emitter.events))
	}
	event := emitter.events[0]
	if event.Service != "edge-gateway" || event.TraceID == "" || event.Level != sharedlogging.LevelInfo {
		t.Fatalf("event = %#v", event)
	}
	payload, err := json.Marshal(event.Metadata)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), "super-secret") || strings.Contains(string(payload), "credential") || event.Metadata["path"] != "/public" {
		t.Fatalf("metadata = %s", payload)
	}
}

func TestAccessLogFailureIsObservedWithoutChangingResponse(t *testing.T) {
	emitter := &recordingEmitter{accepted: false}
	observations := make([]Observation, 0, 2)
	gateway, err := New(
		WithRoutes(publicRoute()),
		WithAccessLogEmitter("edge-gateway", emitter),
		WithObserver(ObserverFunc(func(_ context.Context, observation Observation) {
			observations = append(observations, observation)
		})),
		withTestUpstreams(map[string]http.Handler{"backend": http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})}),
	)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	gateway.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://gateway/public", nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d", response.Code)
	}
	if len(observations) != 2 || observations[0].Kind != ObservationAccessLogFailure || observations[1].Kind != ObservationRequestCompleted {
		t.Fatalf("observations = %#v", observations)
	}
}

func TestObserverPanicDoesNotAffectResponse(t *testing.T) {
	gateway, err := New(
		WithRoutes(publicRoute()),
		WithObserver(ObserverFunc(func(context.Context, Observation) { panic("observer failed") })),
		withTestUpstreams(map[string]http.Handler{"backend": http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})}),
	)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	gateway.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://gateway/public", nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestHealthEndpointsBypassRoutesAndSkipSuccessfulAccessLog(t *testing.T) {
	emitter := &recordingEmitter{accepted: true}
	proxied := false
	gateway, err := New(
		WithRoutes(publicRoute()),
		WithHealth(HealthConfig{Service: "edge-gateway"}),
		WithAccessLogEmitter("edge-gateway", emitter),
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
	if proxied || len(emitter.events) != 0 {
		t.Fatalf("proxied = %v, events = %d", proxied, len(emitter.events))
	}
}

func TestReadinessFailureReturns503AndIsObserved(t *testing.T) {
	emitter := &recordingEmitter{accepted: true}
	observations := make([]Observation, 0, 2)
	gateway, err := New(
		WithRoutes(publicRoute()),
		WithHealth(HealthConfig{
			Readiness: ReadinessCheckerFunc(func(context.Context) error { return errors.New("dependency unavailable") }),
		}),
		WithAccessLogEmitter("edge-gateway", emitter),
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
	if response.Code != http.StatusServiceUnavailable || len(emitter.events) != 1 {
		t.Fatalf("status = %d, events = %d", response.Code, len(emitter.events))
	}
	if got := emitter.events[0].Metadata["error_code"]; got != "readiness_failed" {
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

func TestComponentPanicReturns500AndProducesAccessLog(t *testing.T) {
	emitter := &recordingEmitter{accepted: true}
	gateway, err := New(
		WithRouteResolver(RouteResolverFunc(func(*http.Request) (Route, bool, error) {
			panic("route resolver failed")
		})),
		WithAccessLogEmitter("edge-gateway", emitter),
		withTestUpstreams(map[string]http.Handler{"backend": http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})}),
	)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	gateway.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://gateway/public", nil))
	if response.Code != http.StatusInternalServerError || len(emitter.events) != 1 {
		t.Fatalf("status = %d, events = %d", response.Code, len(emitter.events))
	}
	if emitter.events[0].Metadata["error_code"] != "gateway_panic" {
		t.Fatalf("metadata = %#v", emitter.events[0].Metadata)
	}
}

func TestChunkedRequestAboveRouteLimitReturns413(t *testing.T) {
	route := publicRoute()
	route.MaxBodyBytes = 4
	gateway, err := New(
		WithRoutes(route),
		WithUpstreams(Upstream{Name: "backend", URL: "http://backend.internal"}),
		WithTransport(roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			_, readErr := io.ReadAll(r.Body)
			return nil, readErr
		})),
	)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "http://gateway/public", strings.NewReader("too large"))
	request.ContentLength = -1
	response := httptest.NewRecorder()
	gateway.ServeHTTP(response, request)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

type recordingEmitter struct {
	accepted bool
	events   []sharedlogging.Event
}

func (emitter *recordingEmitter) Emit(_ context.Context, event sharedlogging.Event) bool {
	emitter.events = append(emitter.events, event)
	return emitter.accepted
}
