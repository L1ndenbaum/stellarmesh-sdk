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

func (ingestor *fakeIngestor) Ingest(_ context.Context, events []sharedlogging.Event) error {
	ingestor.events = append(ingestor.events, events...)
	return ingestor.err
}

func TestRouterAuthenticatesAndAcceptsBatch(t *testing.T) {
	ingestor := &fakeIngestor{}
	router := NewRouter(NewHandler(ingestor, "token"))
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
	router := NewRouter(NewHandler(&fakeIngestor{}, "token"))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/log-events", strings.NewReader("{}")))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", recorder.Code)
	}
}

func TestHandlerMapsQueueFullToUnavailable(t *testing.T) {
	router := NewRouter(NewHandler(&fakeIngestor{err: application.ErrQueueFull}, "token"))
	payload, _ := json.Marshal(sharedlogging.IngestRequest{Event: validEvent(t)})
	request := httptest.NewRequest(http.MethodPost, "/v1/log-events", strings.NewReader(string(payload)))
	request.Header.Set(serviceTokenHeader, "token")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", recorder.Code)
	}
}

func TestHandlerRejectsUnknownFields(t *testing.T) {
	router := NewRouter(NewHandler(&fakeIngestor{err: errors.New("unexpected")}, "token"))
	request := httptest.NewRequest(http.MethodPost, "/v1/log-events", strings.NewReader(`{"unknown":true}`))
	request.Header.Set(serviceTokenHeader, "token")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", recorder.Code)
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
