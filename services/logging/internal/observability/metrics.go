// Package observability exposes bounded Prometheus metrics and readiness state.
package observability

import (
	"net/http"
	"strconv"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics owns one isolated registry for the logging ingester.
type Metrics struct {
	registry     *prometheus.Registry
	ready        atomic.Bool
	httpRequests *prometheus.CounterVec
	ingestEvents *prometheus.CounterVec
	queueDepth   prometheus.Gauge
	kafkaPublish *prometheus.CounterVec
	spoolBytes   *prometheus.GaugeVec
	spoolEvents  *prometheus.CounterVec
	spoolReplay  *prometheus.CounterVec
}

// NewMetrics creates and registers the logging ingester metrics.
func NewMetrics() *Metrics {
	metrics := &Metrics{
		registry: prometheus.NewRegistry(),
		httpRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "stellarmesh", Subsystem: "logging_ingester", Name: "http_requests_total",
			Help: "Logging ingester HTTP requests by route and status.",
		}, []string{"route", "status"}),
		ingestEvents: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "stellarmesh", Subsystem: "logging_ingester", Name: "ingest_events_total",
			Help: "Events accepted or rejected by the in-memory queue.",
		}, []string{"result", "reason"}),
		queueDepth: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "stellarmesh", Subsystem: "logging_ingester", Name: "queue_events",
			Help: "Events currently waiting in the ingestion queue.",
		}),
		kafkaPublish: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "stellarmesh", Subsystem: "logging_ingester", Name: "kafka_publish_events_total",
			Help: "Events passed to Kafka publishing attempts.",
		}, []string{"result"}),
		spoolBytes: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "stellarmesh", Subsystem: "logging_ingester", Name: "spool_bytes",
			Help: "Bytes retained in regular and priority spool segments.",
		}, []string{"priority"}),
		spoolEvents: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "stellarmesh", Subsystem: "logging_ingester", Name: "spool_write_events_total",
			Help: "Events written or rejected by the fallback spool.",
		}, []string{"priority", "result"}),
		spoolReplay: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "stellarmesh", Subsystem: "logging_ingester", Name: "spool_replay_events_total",
			Help: "Events processed by fallback replay attempts.",
		}, []string{"priority", "result"}),
	}
	metrics.registry.MustRegister(
		metrics.httpRequests, metrics.ingestEvents, metrics.queueDepth, metrics.kafkaPublish,
		metrics.spoolBytes, metrics.spoolEvents, metrics.spoolReplay,
	)
	metrics.spoolBytes.WithLabelValues("regular").Set(0)
	metrics.spoolBytes.WithLabelValues("priority").Set(0)
	return metrics
}

// Handler exposes this process's Prometheus registry.
func (metrics *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(metrics.registry, promhttp.HandlerOpts{})
}

// SetReady updates the process readiness state.
func (metrics *Metrics) SetReady(ready bool) {
	metrics.ready.Store(ready)
}

// Ready reports whether the process should continue receiving traffic.
func (metrics *Metrics) Ready() bool {
	return metrics.ready.Load()
}

// ObserveHTTPRequest records one bounded-route HTTP result.
func (metrics *Metrics) ObserveHTTPRequest(route string, status int) {
	metrics.httpRequests.WithLabelValues(route, strconv.Itoa(status)).Inc()
}

// ObserveIngest records accepted or rejected event counts.
func (metrics *Metrics) ObserveIngest(result, reason string, count int) {
	metrics.ingestEvents.WithLabelValues(result, reason).Add(float64(count))
}

// SetQueueDepth records the number of events still queued.
func (metrics *Metrics) SetQueueDepth(depth int) {
	metrics.queueDepth.Set(float64(depth))
}

// ObserveKafkaPublish records a Kafka batch result by event count.
func (metrics *Metrics) ObserveKafkaPublish(result string, count int) {
	metrics.kafkaPublish.WithLabelValues(result).Add(float64(count))
}

// SetSpoolBytes records retained segment bytes for one priority class.
func (metrics *Metrics) SetSpoolBytes(priority string, size int64) {
	metrics.spoolBytes.WithLabelValues(priority).Set(float64(size))
}

// ObserveSpoolWrite records fallback spool writes.
func (metrics *Metrics) ObserveSpoolWrite(priority, result string, count int) {
	metrics.spoolEvents.WithLabelValues(priority, result).Add(float64(count))
}

// ObserveSpoolReplay records fallback replay attempts.
func (metrics *Metrics) ObserveSpoolReplay(priority, result string, count int) {
	metrics.spoolReplay.WithLabelValues(priority, result).Add(float64(count))
}
