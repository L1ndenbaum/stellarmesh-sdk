// Package observability 暴露 ClickHouse sink 健康状态和有界 Prometheus 指标。
package observability

import (
	"net/http"
	"sync/atomic"

	sharedhttp "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/http/api"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics 持有独立 registry 和当前 sink 就绪状态。
type Metrics struct {
	registry     *prometheus.Registry
	ready        atomic.Bool
	messages     *prometheus.CounterVec
	operations   *prometheus.CounterVec
	pending      prometheus.Gauge
	pendingBytes prometheus.Gauge
}

// NewMetrics 注册 ClickHouse sink 指标集。
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

// SetReady 更新所有持久 sink 阶段当前是否可以继续推进。
func (metrics *Metrics) SetReady(ready bool) {
	metrics.ready.Store(ready)
}

// Ready 报告当前就绪状态。
func (metrics *Metrics) Ready() bool {
	return metrics.ready.Load()
}

// SetPendingMessages 记录当前未提交批次的消息数。
func (metrics *Metrics) SetPendingMessages(count int) {
	metrics.pending.Set(float64(count))
}

// SetPendingBytes 记录当前未提交批次持有的源字节数。
func (metrics *Metrics) SetPendingBytes(size int64) {
	metrics.pendingBytes.Set(float64(size))
}

// ObserveMessages 记录消息处理结果。
func (metrics *Metrics) ObserveMessages(result string, count int) {
	metrics.messages.WithLabelValues(result).Add(float64(count))
}

// ObserveOperation 记录一个阶段的结果。
func (metrics *Metrics) ObserveOperation(operation, result string) {
	metrics.operations.WithLabelValues(operation, result).Inc()
}

// NewRouter 暴露存活、就绪和 Prometheus 端点。
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
