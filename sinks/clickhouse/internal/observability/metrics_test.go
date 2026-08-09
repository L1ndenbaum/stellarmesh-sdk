package observability

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRouterExposesHealthReadinessAndMetrics(t *testing.T) {
	metrics := NewMetrics()
	metrics.ObserveMessages("fetched", 1)
	metrics.ObserveOperation("kafka_fetch", "success")
	metrics.SetPendingMessages(1)
	router := NewRouter(metrics)

	ready := httptest.NewRecorder()
	router.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if ready.Code != http.StatusServiceUnavailable {
		t.Fatalf("readiness status = %d", ready.Code)
	}
	metrics.SetReady(true)
	ready = httptest.NewRecorder()
	router.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if ready.Code != http.StatusOK {
		t.Fatalf("readiness status = %d", ready.Code)
	}

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	for _, metric := range []string{
		"stellarmesh_logging_clickhouse_sink_messages_total",
		"stellarmesh_logging_clickhouse_sink_operations_total",
		"stellarmesh_logging_clickhouse_sink_pending_messages",
	} {
		if !strings.Contains(recorder.Body.String(), metric) {
			t.Fatalf("metrics output missing %s", metric)
		}
	}
}
