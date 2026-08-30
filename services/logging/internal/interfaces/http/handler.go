// Package httpapi 暴露日志 v1 HTTP API。
package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/http/jsonbody"
	sharedlogging "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging"
	"github.com/L1ndenbaum/stellarmesh-sdk/services/logging/internal/application"
)

// Ingestor 是 HTTP handler 使用的应用层边界。
type Ingestor interface {
	Ingest(context.Context, []sharedlogging.Event) error
}

// Authenticator 从不透明 token 解析服务身份。
type Authenticator interface {
	Authenticate(string) (string, bool)
}

// Readiness 报告日志接收是否仍能保证持久性。
type Readiness interface {
	Ready() bool
}

// Handler 将 v1 HTTP 请求映射到日志接收应用。
type Handler struct {
	ingestor      Ingestor
	authenticator Authenticator
	readiness     Readiness
}

// NewHandler 创建日志 HTTP handler。
func NewHandler(ingestor Ingestor, authenticator Authenticator, readiness Readiness) *Handler {
	return &Handler{ingestor: ingestor, authenticator: authenticator, readiness: readiness}
}

// HandleHealth 报告进程存活状态。
func (handler *Handler) HandleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// HandleReady 报告已接受事件是否仍可投递或持久缓冲。
func (handler *Handler) HandleReady(w http.ResponseWriter, _ *http.Request) {
	if handler.readiness == nil || !handler.readiness.Ready() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

// HandleLogEvent 校验并排队一个事件。
func (handler *Handler) HandleLogEvent(w http.ResponseWriter, r *http.Request) {
	var request ingestRequest
	if !handler.decode(w, r, &request) {
		return
	}
	if !handler.authorizeEvents(w, r, []sharedlogging.Event{request.Event}) {
		return
	}
	if err := handler.ingestor.Ingest(r.Context(), []sharedlogging.Event{request.Event}); err != nil {
		handler.writeIngestError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, sharedlogging.IngestResult{Accepted: 1})
}

// HandleLogEventBatch 校验并排队一个批次。
func (handler *Handler) HandleLogEventBatch(w http.ResponseWriter, r *http.Request) {
	var request batchIngestRequest
	if !handler.decode(w, r, &request) {
		return
	}
	if !handler.authorizeEvents(w, r, request.Events) {
		return
	}
	if err := handler.ingestor.Ingest(r.Context(), request.Events); err != nil {
		handler.writeIngestError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, sharedlogging.IngestResult{Accepted: len(request.Events)})
}

func (handler *Handler) authorizeEvents(w http.ResponseWriter, r *http.Request, events []sharedlogging.Event) bool {
	service, ok := authenticatedService(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid logging service token")
		return false
	}
	for _, event := range events {
		if event.Service != service {
			writeError(w, http.StatusForbidden, "logging token is not authorized for event service")
			return false
		}
	}
	return true
}

func (handler *Handler) decode(w http.ResponseWriter, r *http.Request, target any) bool {
	err := jsonbody.Decode(w, r, target, jsonbody.Options{
		MaxBytes: sharedlogging.MaxHTTPBodyBytesV1, DisallowUnknownFields: true,
	})
	if err == nil {
		return true
	}
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
	} else {
		writeError(w, http.StatusBadRequest, err.Error())
	}
	return false
}

func (handler *Handler) writeIngestError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	if errors.Is(err, application.ErrTooManyEvents) || errors.Is(err, application.ErrEventTooLarge) {
		status = http.StatusRequestEntityTooLarge
	} else if errors.Is(err, application.ErrQueueFull) ||
		errors.Is(err, application.ErrShuttingDown) ||
		errors.Is(err, application.ErrDurabilityUnavailable) ||
		errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		status = http.StatusServiceUnavailable
	}
	writeError(w, status, err.Error())
}
