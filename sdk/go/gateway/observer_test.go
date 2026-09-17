package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestObserverPanicDoesNotAffectResponse(t *testing.T) {
	gateway, err := New(
		WithRoutes(publicRoute()),
		WithObserver(ObserverFunc(func(context.Context, Observation) { panic("observer failed") })),
		withTestUpstreams(map[string]http.Handler{"backend": http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})}),
	)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	gateway.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://gateway/public", nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d", response.Code)
	}
}
