package observability

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMetricsExposeBoundedLabelsAndReadiness(t *testing.T) {
	metrics := NewMetrics()
	if metrics.Ready() {
		t.Fatal("new metrics unexpectedly ready")
	}
	metrics.SetReady(true)
	metrics.ObserveHTTPRequest("/health", http.StatusOK)
	metrics.ObserveIngest("accepted", "", 2)
	metrics.SetQueueDepth(2)
	metrics.ObserveKafkaPublish("success", 2)
	metrics.SetSpoolBytes("regular", 128)
	metrics.ObserveSpoolWrite("regular", "stored", 2)
	metrics.ObserveSpoolReplay("regular", "published", 2)

	recorder := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	for _, name := range []string{
		"stellarmesh_logging_ingester_http_requests_total",
		"stellarmesh_logging_ingester_ingest_events_total",
		"stellarmesh_logging_ingester_queue_events",
		"stellarmesh_logging_ingester_spool_bytes",
	} {
		if !strings.Contains(recorder.Body.String(), name) {
			t.Fatalf("metrics output missing %s", name)
		}
	}
}
