package httpapi

import (
	"context"
	"net/http"

	sharedhttp "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/http/api"
)

const serviceTokenHeader = "X-Logging-Service-Token"

type authenticatedServiceKey struct{}

// NewRouter wires liveness and authenticated ingestion routes.
func NewRouter(handler *Handler) *sharedhttp.Router {
	router := sharedhttp.NewRouter()
	router.Get("/health", handler.HandleHealth)
	router.With(handler.authenticate).Post("/v1/log-events", handler.HandleLogEvent)
	router.With(handler.authenticate).Post("/v1/log-events/batch", handler.HandleLogEventBatch)
	return router
}

func (handler *Handler) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if handler.authenticator == nil {
			sharedhttp.WriteError(w, http.StatusUnauthorized, "invalid logging service token")
			return
		}
		service, ok := handler.authenticator.Authenticate(r.Header.Get(serviceTokenHeader))
		if !ok {
			sharedhttp.WriteError(w, http.StatusUnauthorized, "invalid logging service token")
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
