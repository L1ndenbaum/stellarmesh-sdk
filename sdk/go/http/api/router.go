package api

import "github.com/go-chi/chi/v5"

// Router is the Chi router type used by service entrypoints.
type Router = chi.Mux

// NewRouter returns an empty router.
func NewRouter() *Router {
	return chi.NewRouter()
}
