package observability_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/objectstorage"
	"github.com/L1ndenbaum/stellarmesh-sdk/services/storage/internal/observability"
)

func TestMetricsUseBoundedLabels(t *testing.T) {
	t.Parallel()
	metrics := observability.NewMetrics()
	metrics.ObserveHTTPRequest("/v1/objects/stat", 200)
	metrics.Observe(context.Background(), objectstorage.Observation{
		Operation: "stat", Outcome: objectstorage.OutcomeError, Duration: time.Millisecond,
		ErrorKind: objectstorage.ErrNotFound,
	})
	response := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(response, httptest.NewRequest("GET", "/metrics", nil))
	payload := response.Body.String()
	for _, expected := range []string{`route="/v1/objects/stat"`, `status="200"`, `operation="stat"`, `result="not_found"`} {
		if !strings.Contains(payload, expected) {
			t.Fatalf("指标缺少 %s:\n%s", expected, payload)
		}
	}
	for _, forbidden := range []string{`bucket="`, `principal="`, "signature", `object_key="`, `url="`} {
		if strings.Contains(strings.ToLower(payload), forbidden) {
			t.Fatalf("指标包含禁止标签或内容 %q", forbidden)
		}
	}
}
