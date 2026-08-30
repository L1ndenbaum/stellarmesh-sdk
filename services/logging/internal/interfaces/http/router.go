package httpapi

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
)

const serviceTokenHeader = "X-Logging-Service-Token"

type authenticatedServiceKey struct{}

// Monitoring 提供进程指标端点和有界请求计数器。
type Monitoring interface {
	Handler() http.Handler
	ObserveHTTPRequest(route string, status int)
}

// NewRouter 连接存活检查和带鉴权的日志接收路由。
func NewRouter(handler *Handler, monitoring Monitoring) *chi.Mux {
	router := chi.NewRouter()
	router.With(observe(monitoring, "/health")).Get("/health", handler.HandleHealth)
	router.With(observe(monitoring, "/health/live")).Get("/health/live", handler.HandleHealth)
	router.With(observe(monitoring, "/health/ready")).Get("/health/ready", handler.HandleReady)
	if monitoring != nil {
		router.With(observe(monitoring, "/metrics")).Handle("/metrics", monitoring.Handler())
	}
	router.With(observe(monitoring, "/v1/log-events"), handler.authenticate).
		Post("/v1/log-events", handler.HandleLogEvent)
	router.With(observe(monitoring, "/v1/log-events/batch"), handler.authenticate).
		Post("/v1/log-events/batch", handler.HandleLogEventBatch)
	return router
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (recorder *statusRecorder) WriteHeader(status int) {
	if recorder.status != 0 {
		return
	}
	recorder.status = status
	recorder.ResponseWriter.WriteHeader(status)
}

func (recorder *statusRecorder) Write(payload []byte) (int, error) {
	if recorder.status == 0 {
		recorder.WriteHeader(http.StatusOK)
	}
	return recorder.ResponseWriter.Write(payload)
}

func observe(monitoring Monitoring, route string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			recorder := &statusRecorder{ResponseWriter: w}
			next.ServeHTTP(recorder, r)
			if monitoring != nil {
				status := recorder.status
				if status == 0 {
					status = http.StatusOK
				}
				monitoring.ObserveHTTPRequest(route, status)
			}
		})
	}
}

func (handler *Handler) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if handler.authenticator == nil {
			writeError(w, http.StatusUnauthorized, "invalid logging service token")
			return
		}
		service, ok := handler.authenticator.Authenticate(r.Header.Get(serviceTokenHeader))
		if !ok {
			writeError(w, http.StatusUnauthorized, "invalid logging service token")
			return
		}
		ctx := context.WithValue(r.Context(), authenticatedServiceKey{}, service)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func authenticatedService(ctx context.Context) (string, bool) {
	service, ok := ctx.Value(authenticatedServiceKey{}).(string)
	return service, ok && service != ""
}
