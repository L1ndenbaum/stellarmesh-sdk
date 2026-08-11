package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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
	request := httptest.NewRequest(http.MethodPost, "/v1/log-events/batch", strings.NewReader(string(payload)))
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
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/log-events", strings.NewReader("{}")))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", recorder.Code)
	}
}

func TestHandlerMapsQueueFullToUnavailable(t *testing.T) {
	router := testRouter(&fakeIngestor{err: application.ErrQueueFull})
	payload, _ := json.Marshal(sharedlogging.IngestRequest{Event: validEvent(t)})
	request := httptest.NewRequest(http.MethodPost, "/v1/log-events", strings.NewReader(string(payload)))
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
	request := httptest.NewRequest(http.MethodPost, "/v1/log-events", strings.NewReader(string(payload)))
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
	request := httptest.NewRequest(http.MethodPost, "/v1/log-events", strings.NewReader(string(payload)))
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
	request := httptest.NewRequest(http.MethodPost, "/v1/log-events", strings.NewReader(`{"unknown":true}`))
	request.Header.Set(serviceTokenHeader, "token")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", recorder.Code)
	}
}

func TestHandlerRejectsServiceIdentityMismatch(t *testing.T) {
	router := testRouter(&fakeIngestor{})
	event := validEvent(t)
	event.Service = "another-service"
	payload, _ := json.Marshal(sharedlogging.IngestRequest{Event: event})
	request := httptest.NewRequest(http.MethodPost, "/v1/log-events", strings.NewReader(string(payload)))
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
		EventID: id, Timestamp: time.Now(), Level: sharedlogging.LevelInfo,
		Service: "test", Message: "event", Metadata: map[string]any{},
	}
}
