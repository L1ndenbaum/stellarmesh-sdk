// Package observability exposes ClickHouse sink health and bounded Prometheus metrics.
package observability

import (
	"net/http"
	"sync/atomic"

	sharedhttp "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/http/api"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics owns one isolated registry and the current sink readiness state.
type Metrics struct {
	registry     *prometheus.Registry
	ready        atomic.Bool
	messages     *prometheus.CounterVec
	operations   *prometheus.CounterVec
	pending      prometheus.Gauge
	pendingBytes prometheus.Gauge
}

// NewMetrics registers the ClickHouse sink metric set.
func NewMetrics() *Metrics {
	metrics := &Metrics{
		registry: prometheus.NewRegistry(),
		messages: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "stellarmesh", Subsystem: "logging_clickhouse_sink", Name: "messages_total",
			Help: "Source messages fetched, inserted, or dead-lettered.",
		}, []string{"result"}),
		operations: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "stellarmesh", Subsystem: "logging_clickhouse_sink", Name: "operations_total",
			Help: "Sink stage results by bounded operation name.",
		}, []string{"operation", "result"}),
		pending: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "stellarmesh", Subsystem: "logging_clickhouse_sink", Name: "pending_messages",
			Help: "Source messages held in the current uncommitted batch.",
		}),
		pendingBytes: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "stellarmesh", Subsystem: "logging_clickhouse_sink", Name: "pending_bytes",
			Help: "Kafka key and value bytes held in the current uncommitted batch.",
		}),
	}
	metrics.registry.MustRegister(metrics.messages, metrics.operations, metrics.pending, metrics.pendingBytes)
	return metrics
}

// SetReady updates whether all durable sink stages can currently make progress.
func (metrics *Metrics) SetReady(ready bool) {
	metrics.ready.Store(ready)
}

// Ready reports the current readiness state.
func (metrics *Metrics) Ready() bool {
	return metrics.ready.Load()
}

// SetPendingMessages records the current uncommitted batch size.
func (metrics *Metrics) SetPendingMessages(count int) {
	metrics.pending.Set(float64(count))
}

// SetPendingBytes records source bytes held in the current uncommitted batch.
func (metrics *Metrics) SetPendingBytes(size int64) {
	metrics.pendingBytes.Set(float64(size))
}

// ObserveMessages records message outcomes.
func (metrics *Metrics) ObserveMessages(result string, count int) {
	metrics.messages.WithLabelValues(result).Add(float64(count))
}

// ObserveOperation records one stage result.
func (metrics *Metrics) ObserveOperation(operation, result string) {
	metrics.operations.WithLabelValues(operation, result).Inc()
}

// NewRouter exposes liveness, readiness, and Prometheus endpoints.
func NewRouter(metrics *Metrics) http.Handler {
	mux := http.NewServeMux()
	liveness := func(w http.ResponseWriter, _ *http.Request) {
		sharedhttp.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
	mux.HandleFunc("GET /health", liveness)
	mux.HandleFunc("GET /health/live", liveness)
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, _ *http.Request) {
		if metrics == nil || !metrics.Ready() {
			sharedhttp.WriteJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready"})
			return
		}
		sharedhttp.WriteJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})
	if metrics != nil {
		mux.Handle("GET /metrics", promhttp.HandlerFor(metrics.registry, promhttp.HandlerOpts{}))
	}
	return mux
}
