// Package httpapi exposes the logging v1 HTTP API.
package httpapi

import (
	"context"
	"errors"
	"net/http"

	sharedhttp "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/http/api"
	sharedlogging "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging"
	"github.com/L1ndenbaum/stellarmesh-sdk/services/logging/internal/application"
)

// Ingestor is the application boundary used by HTTP handlers.
type Ingestor interface {
	Ingest(context.Context, []sharedlogging.Event) error
}

// Handler maps v1 HTTP requests to the ingestion application.
type Handler struct {
	ingestor     Ingestor
	serviceToken string
}

// NewHandler creates a logging HTTP handler.
func NewHandler(ingestor Ingestor, serviceToken string) *Handler {
	return &Handler{ingestor: ingestor, serviceToken: serviceToken}
}

// HandleHealth reports process liveness.
func (handler *Handler) HandleHealth(w http.ResponseWriter, _ *http.Request) {
	sharedhttp.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// HandleLogEvent validates and queues one event.
func (handler *Handler) HandleLogEvent(w http.ResponseWriter, r *http.Request) {
	var request ingestRequest
	if !handler.decode(w, r, &request) {
		return
	}
	if err := handler.ingestor.Ingest(r.Context(), []sharedlogging.Event{request.Event}); err != nil {
		handler.writeIngestError(w, err)
		return
	}
	sharedhttp.WriteJSON(w, http.StatusAccepted, sharedlogging.IngestResult{Accepted: 1})
}

// HandleLogEventBatch validates and queues a batch.
func (handler *Handler) HandleLogEventBatch(w http.ResponseWriter, r *http.Request) {
	var request batchIngestRequest
	if !handler.decode(w, r, &request) {
		return
	}
	if err := handler.ingestor.Ingest(r.Context(), request.Events); err != nil {
		handler.writeIngestError(w, err)
		return
	}
	sharedhttp.WriteJSON(w, http.StatusAccepted, sharedlogging.IngestResult{Accepted: len(request.Events)})
}

func (handler *Handler) decode(w http.ResponseWriter, r *http.Request, target any) bool {
	err := sharedhttp.DecodeJSONWithOptions(w, r, target, sharedhttp.DecodeJSONOptions{
		MaxBytes: sharedhttp.DefaultJSONBodyLimit, DisallowUnknownFields: true,
	})
	if err == nil {
		return true
	}
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		sharedhttp.WriteError(w, http.StatusRequestEntityTooLarge, "request body too large")
	} else {
		sharedhttp.WriteError(w, http.StatusBadRequest, err.Error())
	}
	return false
}

func (handler *Handler) writeIngestError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	if errors.Is(err, application.ErrQueueFull) ||
		errors.Is(err, application.ErrShuttingDown) ||
		errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		status = http.StatusServiceUnavailable
	}
	sharedhttp.WriteError(w, status, err.Error())
}
