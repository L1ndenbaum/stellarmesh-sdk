package httpapi

import sharedhttp "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/http/api"

const serviceTokenHeader = "X-Logging-Service-Token"

// NewRouter wires liveness and authenticated ingestion routes.
func NewRouter(handler *Handler) *sharedhttp.Router {
	router := sharedhttp.NewRouter()
	auth := sharedhttp.TokenAuth(sharedhttp.TokenAuthConfig{
		Header: serviceTokenHeader, Token: handler.serviceToken, Message: "invalid logging service token",
	})
	router.Get("/health", handler.HandleHealth)
	router.With(auth).Post("/v1/log-events", handler.HandleLogEvent)
	router.With(auth).Post("/v1/log-events/batch", handler.HandleLogEventBatch)
	return router
}
