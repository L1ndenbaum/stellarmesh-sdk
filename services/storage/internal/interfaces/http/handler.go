// Package httpapi 暴露 Storage v1 控制面 HTTP API。
package httpapi

import (
	"github.com/L1ndenbaum/stellarmesh-sdk/services/storage/internal/application"
	"github.com/L1ndenbaum/stellarmesh-sdk/services/storage/internal/storagev1"
)

// Readiness 报告全部 namespace 最近一次可访问性检查结果。
type Readiness interface {
	Ready() bool
}

// Handler 将严格 Storage v1 请求映射到 namespace store。
type Handler struct {
	registry  *application.Registry
	policy    *storagev1.Policy
	readiness Readiness
}

// NewHandler 创建 storage-service HTTP handler。
func NewHandler(registry *application.Registry, policy *storagev1.Policy, readiness Readiness) *Handler {
	return &Handler{registry: registry, policy: policy, readiness: readiness}
}
