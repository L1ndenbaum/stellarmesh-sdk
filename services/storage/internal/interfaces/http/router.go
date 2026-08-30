package httpapi

import (
	"net/http"

	"github.com/L1ndenbaum/stellarmesh-sdk/services/storage/internal/storagev1"
	"github.com/go-chi/chi/v5"
)

// Monitoring 提供指标端点和有界请求计数器。
type Monitoring interface {
	Handler() http.Handler
	ObserveHTTPRequest(route string, status int)
}

// NewRouter 连接健康端点和 fail-close 的受保护控制面路由。
func NewRouter(handler *Handler, monitoring Monitoring) *chi.Mux {
	router := chi.NewRouter()
	router.With(observe(monitoring, "/health")).Get("/health", handler.HandleHealth)
	router.With(observe(monitoring, "/health/live")).Get("/health/live", handler.HandleHealth)
	router.With(observe(monitoring, "/health/ready")).Get("/health/ready", handler.HandleReady)
	if monitoring != nil {
		router.With(observe(monitoring, "/metrics")).Handle("/metrics", monitoring.Handler())
	}
	protected := []struct {
		route   string
		handler http.HandlerFunc
	}{
		{route: "/v1/objects/stat", handler: handler.HandleStat},
		{route: "/v1/objects/delete", handler: handler.HandleDelete},
		{route: "/v1/presign/get", handler: handler.HandlePresignGet},
		{route: "/v1/presign/put", handler: handler.HandlePresignPut},
		{route: "/v1/multipart/create", handler: handler.HandleMultipartCreate},
		{route: "/v1/multipart/presign-part", handler: handler.HandleMultipartPresignPart},
		{route: "/v1/multipart/complete", handler: handler.HandleMultipartComplete},
		{route: "/v1/multipart/abort", handler: handler.HandleMultipartAbort},
	}
	for _, item := range protected {
		router.With(observe(monitoring, item.route), handler.authenticate, handler.requireReady).Post(item.route, item.handler)
	}
	return router
}

func (handler *Handler) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if handler.policy == nil || !handler.policy.Authenticate(request.Header.Get(storagev1.ServiceTokenHeader)) {
			writeError(w, http.StatusUnauthorized, "invalid storage service token")
			return
		}
		next.ServeHTTP(w, request)
	})
}

func (handler *Handler) requireReady(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if handler.readiness == nil || !handler.readiness.Ready() {
			writeError(w, http.StatusServiceUnavailable, "storage unavailable")
			return
		}
		next.ServeHTTP(w, request)
	})
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
		return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
			recorder := &statusRecorder{ResponseWriter: w}
			next.ServeHTTP(recorder, request)
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
