package gateway

import (
	"context"
	"errors"
	"time"
)

// ObservationKind 区分请求完成和旁路组件故障。
type ObservationKind string

const (
	ObservationRequestCompleted ObservationKind = "request_completed"
	ObservationComponentFailure ObservationKind = "component_failure"
	ObservationAccessLogFailure ObservationKind = "access_log_failure"
)

// Observation 是可供指标适配器消费的低基数网关事件。
type Observation struct {
	Kind      ObservationKind
	Component string
	Route     string
	Upstream  string
	Status    int
	Elapsed   time.Duration
}

// Observer 接收网关观测事件；实现不得依赖请求能否继续转发。
type Observer interface {
	Observe(context.Context, Observation)
}

// ObserverFunc 让函数直接实现 Observer。
type ObserverFunc func(context.Context, Observation)

// Observe 调用观测函数。
func (observer ObserverFunc) Observe(ctx context.Context, observation Observation) {
	observer(ctx, observation)
}

// WithObserver 启用不影响请求结果的低基数观测事件。
func WithObserver(observer Observer) Option {
	return componentOption("observer", func(config *config) error {
		if isNilInterface(observer) {
			return errors.New("gateway observer is nil")
		}
		config.observer = observer
		return nil
	})
}

func (gateway *Gateway) safeObserve(ctx context.Context, observation Observation) {
	if gateway.observer == nil {
		return
	}
	defer func() {
		_ = recover()
	}()
	gateway.observer.Observe(ctx, observation)
}
