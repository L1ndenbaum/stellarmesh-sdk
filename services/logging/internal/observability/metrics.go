// Package observability 暴露有界 Prometheus 指标和就绪状态。
package observability

import (
	"net/http"
	"strconv"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics 为日志接收服务持有独立 registry。
type Metrics struct {
	registry      *prometheus.Registry
	ready         atomic.Bool
	httpRequests  *prometheus.CounterVec
	ingestEvents  *prometheus.CounterVec
	queueDepth    prometheus.Gauge
	queueBytes    prometheus.Gauge
	kafkaPublish  *prometheus.CounterVec
	consoleEvents *prometheus.CounterVec
	spoolBytes    *prometheus.GaugeVec
	spoolEvents   *prometheus.CounterVec
	spoolReplay   *prometheus.CounterVec
}

// NewMetrics 创建并注册日志接收服务指标。
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
		queueBytes: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "stellarmesh", Subsystem: "logging_ingester", Name: "queue_bytes",
			Help: "Serialized event bytes waiting for durable ingestion acknowledgement.",
		}),
		kafkaPublish: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "stellarmesh", Subsystem: "logging_ingester", Name: "kafka_publish_events_total",
			Help: "Events passed to Kafka publishing attempts.",
		}, []string{"result"}),
		consoleEvents: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "stellarmesh", Subsystem: "logging_ingester", Name: "console_events_total",
			Help: "Durably accepted event copies processed by the asynchronous console sink.",
		}, []string{"result"}),
		spoolBytes: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "stellarmesh", Subsystem: "logging_ingester", Name: "spool_bytes",
			Help: "Bytes retained in regular, priority, and quarantine spool storage.",
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
		metrics.httpRequests, metrics.ingestEvents, metrics.queueDepth, metrics.queueBytes, metrics.kafkaPublish,
		metrics.consoleEvents, metrics.spoolBytes, metrics.spoolEvents, metrics.spoolReplay,
	)
	metrics.spoolBytes.WithLabelValues("regular").Set(0)
	metrics.spoolBytes.WithLabelValues("priority").Set(0)
	metrics.spoolBytes.WithLabelValues("quarantine").Set(0)
	return metrics
}

// Handler 暴露当前进程的 Prometheus registry。
func (metrics *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(metrics.registry, promhttp.HandlerOpts{})
}

// SetReady 更新进程就绪状态。
func (metrics *Metrics) SetReady(ready bool) {
	metrics.ready.Store(ready)
}

// Ready 报告进程是否应继续接收流量。
func (metrics *Metrics) Ready() bool {
	return metrics.ready.Load()
}

// ObserveHTTPRequest 记录一个有界路由的 HTTP 结果。
func (metrics *Metrics) ObserveHTTPRequest(route string, status int) {
	metrics.httpRequests.WithLabelValues(route, strconv.Itoa(status)).Inc()
}

// ObserveIngest 记录已接受或已拒绝的事件数。
func (metrics *Metrics) ObserveIngest(result, reason string, count int) {
	metrics.ingestEvents.WithLabelValues(result, reason).Add(float64(count))
}

// SetQueueDepth 记录仍在队列中的事件数。
func (metrics *Metrics) SetQueueDepth(depth int) {
	metrics.queueDepth.Set(float64(depth))
}

// SetQueueBytes 记录仍在等待持久确认的序列化字节数。
func (metrics *Metrics) SetQueueBytes(size int64) {
	metrics.queueBytes.Set(float64(size))
}

// ObserveKafkaPublish 按事件数记录 Kafka 批次结果。
func (metrics *Metrics) ObserveKafkaPublish(result string, count int) {
	metrics.kafkaPublish.WithLabelValues(result).Add(float64(count))
}

// ObserveConsole 记录异步控制台副本的有限结果。
func (metrics *Metrics) ObserveConsole(result string, count int) {
	metrics.consoleEvents.WithLabelValues(result).Add(float64(count))
}

// SetSpoolBytes 记录一个优先级类别保留的分段字节数。
func (metrics *Metrics) SetSpoolBytes(priority string, size int64) {
	metrics.spoolBytes.WithLabelValues(priority).Set(float64(size))
}

// ObserveSpoolWrite 记录 fallback spool 写入。
func (metrics *Metrics) ObserveSpoolWrite(priority, result string, count int) {
	metrics.spoolEvents.WithLabelValues(priority, result).Add(float64(count))
}

// ObserveSpoolReplay 记录 fallback 回放尝试。
func (metrics *Metrics) ObserveSpoolReplay(priority, result string, count int) {
	metrics.spoolReplay.WithLabelValues(priority, result).Add(float64(count))
}
