// Package application 编排 namespace store 和就绪检查。
package application

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/objectstorage"
)

// Store 是 storage-service 依赖的完整对象存储边界。
type Store interface {
	objectstorage.Checker
	objectstorage.Reader
	objectstorage.Writer
	objectstorage.Presigner
	objectstorage.MultipartStore
}

// Registry 按逻辑 namespace 保存已绑定的 store。
type Registry struct {
	stores map[string]Store
}

// NewRegistry 复制 store map，防止调用方后续改写注册表。
func NewRegistry(stores map[string]Store) *Registry {
	copyStores := make(map[string]Store, len(stores))
	for name, store := range stores {
		copyStores[name] = store
	}
	return &Registry{stores: copyStores}
}

// Store 返回一个 namespace store。
func (registry *Registry) Store(namespace string) (Store, bool) {
	store, exists := registry.stores[namespace]
	return store, exists
}

// Health 持有全部 namespace 的 fail-close readiness。
type Health struct {
	registry *Registry
	ready    atomic.Bool
}

// NewHealth 创建初始为 not-ready 的健康检查器。
func NewHealth(registry *Registry) *Health {
	return &Health{registry: registry}
}

// Ready 报告最近一次全量检查是否全部成功。
func (health *Health) Ready() bool {
	return health != nil && health.ready.Load()
}

// SetReady 用于关闭流程在停止接流量前立即降为 not-ready。
func (health *Health) SetReady(ready bool) {
	if health != nil {
		health.ready.Store(ready)
	}
}

// CheckAll 并发检查每个 namespace，每项使用独立超时。
func (health *Health) CheckAll(ctx context.Context, timeout time.Duration) bool {
	if health == nil || health.registry == nil || len(health.registry.stores) == 0 {
		return false
	}
	var wait sync.WaitGroup
	results := make(chan bool, len(health.registry.stores))
	for _, store := range health.registry.stores {
		store := store
		wait.Add(1)
		go func() {
			defer wait.Done()
			checkCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			results <- store.Check(checkCtx) == nil
		}()
	}
	wait.Wait()
	close(results)
	allReady := true
	for ready := range results {
		allReady = allReady && ready
	}
	health.ready.Store(allReady)
	return allReady
}

// Run 立即检查一次，并按固定间隔持续刷新 readiness。
func (health *Health) Run(ctx context.Context, timeout, interval time.Duration) {
	health.CheckAll(ctx, timeout)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			health.ready.Store(false)
			return
		case <-ticker.C:
			health.CheckAll(ctx, timeout)
		}
	}
}
