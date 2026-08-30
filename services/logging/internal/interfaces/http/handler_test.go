package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sharedlogging "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging"
	"github.com/L1ndenbaum/stellarmesh-sdk/services/logging/internal/application"
)

type fakeIngestor struct {
	err    error
	events []sharedlogging.Event
}

type fakeAuthenticator map[string]string

type fakeMonitoring struct {
	ready    bool
	requests map[string]int
}

func (monitoring *fakeMonitoring) Ready() bool { return monitoring.ready }

func (monitoring *fakeMonitoring) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("test_metric 1\n"))
	})
}

func (monitoring *fakeMonitoring) ObserveHTTPRequest(route string, status int) {
	if monitoring.requests == nil {
		monitoring.requests = map[string]int{}
	}
	monitoring.requests[route+":"+http.StatusText(status)]++
}

func (authenticator fakeAuthenticator) Authenticate(token string) (string, bool) {
	service, ok := authenticator[token]
	return service, ok
}

func testRouter(ingestor Ingestor) http.Handler {
	monitoring := &fakeMonitoring{ready: true}
	return NewRouter(NewHandler(ingestor, fakeAuthenticator{"token": "test"}, monitoring), monitoring)
}

func (ingestor *fakeIngestor) Ingest(_ context.Context, events []sharedlogging.Event) error {
	ingestor.events = append(ingestor.events, events...)
	return ingestor.err
}

func TestRouterAuthenticatesAndAcceptsBatch(t *testing.T) {
	ingestor := &fakeIngestor{}
	router := testRouter(ingestor)
	payload, err := json.Marshal(sharedlogging.BatchIngestRequest{Events: []sharedlogging.Event{validEvent(t)}})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v2/log-events/batch", strings.NewReader(string(payload)))
	request.Header.Set(serviceTokenHeader, "token")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted || len(ingestor.events) != 1 {
		t.Fatalf("status=%d events=%d body=%s", recorder.Code, len(ingestor.events), recorder.Body.String())
	}
}

func TestRouterRejectsMissingToken(t *testing.T) {
	router := testRouter(&fakeIngestor{})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v2/log-events", strings.NewReader("{}")))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", recorder.Code)
	}
}

func TestRouterDoesNotExposeV1IngestRoutes(t *testing.T) {
	router := testRouter(&fakeIngestor{})
	for _, path := range []string{"/v1/log-events", "/v1/log-events/batch"} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}"))
		request.Header.Set(serviceTokenHeader, "token")
		router.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("path=%s status=%d", path, recorder.Code)
		}
	}
}

func TestHandlerMapsQueueFullToUnavailable(t *testing.T) {
	router := testRouter(&fakeIngestor{err: application.ErrQueueFull})
	payload, _ := json.Marshal(sharedlogging.IngestRequest{Event: validEvent(t)})
	request := httptest.NewRequest(http.MethodPost, "/v2/log-events", strings.NewReader(string(payload)))
	request.Header.Set(serviceTokenHeader, "token")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", recorder.Code)
	}
}

func TestHandlerMapsDurabilityFailureToUnavailable(t *testing.T) {
	router := testRouter(&fakeIngestor{err: application.ErrDurabilityUnavailable})
	payload, _ := json.Marshal(sharedlogging.IngestRequest{Event: validEvent(t)})
	request := httptest.NewRequest(http.MethodPost, "/v2/log-events", strings.NewReader(string(payload)))
	request.Header.Set(serviceTokenHeader, "token")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", recorder.Code)
	}
}

func TestHandlerMapsTooManyEventsToPayloadTooLarge(t *testing.T) {
	router := testRouter(&fakeIngestor{err: application.ErrTooManyEvents})
	payload, _ := json.Marshal(sharedlogging.IngestRequest{Event: validEvent(t)})
	request := httptest.NewRequest(http.MethodPost, "/v2/log-events", strings.NewReader(string(payload)))
	request.Header.Set(serviceTokenHeader, "token")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d", recorder.Code)
	}
}

func TestHandlerMapsOversizedEventToPayloadTooLarge(t *testing.T) {
	router := testRouter(&fakeIngestor{err: application.ErrEventTooLarge})
	payload, _ := json.Marshal(sharedlogging.IngestRequest{Event: validEvent(t)})
	request := httptest.NewRequest(http.MethodPost, "/v2/log-events", strings.NewReader(string(payload)))
	request.Header.Set(serviceTokenHeader, "token")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d", recorder.Code)
	}
}

func TestRouterExposesLivenessReadinessAndMetrics(t *testing.T) {
	monitoring := &fakeMonitoring{ready: false}
	router := NewRouter(NewHandler(&fakeIngestor{}, fakeAuthenticator{}, monitoring), monitoring)
	for path, wantStatus := range map[string]int{
		"/health": http.StatusOK, "/health/live": http.StatusOK,
		"/health/ready": http.StatusServiceUnavailable, "/metrics": http.StatusOK,
	} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != wantStatus {
			t.Fatalf("path=%s status=%d", path, recorder.Code)
		}
	}
	if monitoring.requests["/health/ready:Service Unavailable"] != 1 {
		t.Fatalf("requests = %v", monitoring.requests)
	}
}

func TestHandlerRejectsUnknownFields(t *testing.T) {
	router := testRouter(&fakeIngestor{err: errors.New("unexpected")})
	request := httptest.NewRequest(http.MethodPost, "/v2/log-events", strings.NewReader(`{"unknown":true}`))
	request.Header.Set(serviceTokenHeader, "token")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", recorder.Code)
	}
}

func TestHandlerRejectsSharedInvalidEventFixtures(t *testing.T) {
	payload, err := os.ReadFile(filepath.Join(
		"..", "..", "..", "..", "..", "contracts", "logging", "v2", "testdata", "invalid-events.json",
	))
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Name    string          `json:"name"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(payload, &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range fixtures {
		for _, endpoint := range []struct {
			name string
			path string
			body any
		}{
			{name: "single", path: "/v2/log-events", body: map[string]any{"event": fixture.Payload}},
			{name: "batch", path: "/v2/log-events/batch", body: map[string]any{"events": []json.RawMessage{fixture.Payload}}},
		} {
			t.Run(fixture.Name+"/"+endpoint.name, func(t *testing.T) {
				body, err := json.Marshal(endpoint.body)
				if err != nil {
					t.Fatal(err)
				}
				request := httptest.NewRequest(http.MethodPost, endpoint.path, bytes.NewReader(body))
				request.Header.Set(serviceTokenHeader, "token")
				recorder := httptest.NewRecorder()
				testRouter(&fakeIngestor{}).ServeHTTP(recorder, request)
				if recorder.Code != http.StatusBadRequest {
					t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
				}
			})
		}
	}
}

func TestHandlerRejectsServiceIdentityMismatch(t *testing.T) {
	router := testRouter(&fakeIngestor{})
	event := validEvent(t)
	event.Service = "another-service"
	payload, _ := json.Marshal(sharedlogging.IngestRequest{Event: event})
	request := httptest.NewRequest(http.MethodPost, "/v2/log-events", strings.NewReader(string(payload)))
	request.Header.Set(serviceTokenHeader, "token")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func validEvent(t *testing.T) sharedlogging.Event {
	t.Helper()
	id, err := sharedlogging.NewEventID()
	if err != nil {
		t.Fatal(err)
	}
	return sharedlogging.Event{
		EventID: id, Timestamp: time.Now(), Kind: sharedlogging.EventKindLog, Level: sharedlogging.LevelInfo,
		Service: "test", Message: "event", Metadata: map[string]any{},
	}
}
