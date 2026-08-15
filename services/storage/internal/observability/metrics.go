// Package observability 暴露有界 Prometheus 指标。
package observability

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/objectstorage"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics 为 storage-service 持有独立 registry。
type Metrics struct {
	registry     *prometheus.Registry
	httpRequests *prometheus.CounterVec
	operations   *prometheus.CounterVec
	durations    *prometheus.HistogramVec
	bytes        *prometheus.CounterVec
}

// NewMetrics 创建仅含有界标签的指标集合。
func NewMetrics() *Metrics {
	metrics := &Metrics{
		registry: prometheus.NewRegistry(),
		httpRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "stellarmesh", Subsystem: "storage_service", Name: "http_requests_total",
			Help: "Storage control-plane HTTP requests by route and status.",
		}, []string{"route", "status"}),
		operations: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "stellarmesh", Subsystem: "storage_service", Name: "operations_total",
			Help: "S3 operations by bounded operation and result.",
		}, []string{"operation", "result"}),
		durations: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "stellarmesh", Subsystem: "storage_service", Name: "operation_duration_seconds",
			Help: "S3 operation duration by bounded operation and result.",
		}, []string{"operation", "result"}),
		bytes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "stellarmesh", Subsystem: "storage_service", Name: "operation_bytes_total",
			Help: "Declared or returned bytes by bounded operation and result.",
		}, []string{"operation", "result"}),
	}
	metrics.registry.MustRegister(metrics.httpRequests, metrics.operations, metrics.durations, metrics.bytes)
	return metrics
}

// Handler 暴露当前进程的 Prometheus registry。
func (metrics *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(metrics.registry, promhttp.HandlerOpts{})
}

// ObserveHTTPRequest 记录固定路由和状态码。
func (metrics *Metrics) ObserveHTTPRequest(route string, status int) {
	metrics.httpRequests.WithLabelValues(route, strconv.Itoa(status)).Inc()
}

// Observe 实现 objectstorage.Observer，且不接收敏感对象标识。
func (metrics *Metrics) Observe(_ context.Context, observation objectstorage.Observation) {
	result := "success"
	if observation.Outcome == objectstorage.OutcomeError {
		result = errorResult(observation.ErrorKind)
	}
	metrics.operations.WithLabelValues(observation.Operation, result).Inc()
	metrics.durations.WithLabelValues(observation.Operation, result).Observe(observation.Duration.Seconds())
	if observation.Bytes > 0 {
		metrics.bytes.WithLabelValues(observation.Operation, result).Add(float64(observation.Bytes))
	}
}

func errorResult(err error) string {
	switch {
	case errors.Is(err, objectstorage.ErrNotFound):
		return "not_found"
	case errors.Is(err, objectstorage.ErrForbidden):
		return "forbidden"
	case errors.Is(err, objectstorage.ErrPreconditionFailed):
		return "precondition_failed"
	case errors.Is(err, objectstorage.ErrConflict):
		return "conflict"
	case errors.Is(err, objectstorage.ErrInvalidArgument):
		return "invalid_argument"
	default:
		return "unavailable"
	}
}

var _ objectstorage.Observer = (*Metrics)(nil)
