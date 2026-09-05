package gateway

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"reflect"
	"time"
)

type accessLogState struct {
	AccessLog
	StartedAt        time.Time
	FailureComponent string
	SkipSuccessful   bool
}

func newAccessLogState(r *http.Request) *accessLogState {
	startedAt := time.Now().UTC()
	return &accessLogState{
		AccessLog: AccessLog{
			Timestamp:       startedAt,
			Method:          r.Method,
			Path:            r.URL.Path,
			AuthResult:      "skipped",
			RateLimitResult: make(map[RateLimitScope]string),
		},
		StartedAt: startedAt,
	}
}

func (gateway *Gateway) finishRequest(ctx context.Context, recorder *responseRecorder, state *accessLogState) {
	state.Status = recorder.Status()
	state.Elapsed = time.Since(state.StartedAt)
	if state.ErrorCode == "" {
		state.ErrorCode = defaultErrorCode(state.Status)
	}
	background := context.WithoutCancel(ctx)
	if state.FailureComponent != "" {
		gateway.safeObserve(background, Observation{
			Kind: ObservationComponentFailure, Component: state.FailureComponent,
			Route: state.Route, Upstream: state.Upstream, Status: state.Status, Elapsed: state.Elapsed,
		})
	}
	if gateway.accessLogger != nil && !(state.SkipSuccessful && state.Status < http.StatusBadRequest) {
		if err := callAccessLogger(background, gateway.accessLogger, cloneAccessLog(state.AccessLog)); err != nil {
			gateway.safeObserve(background, Observation{
				Kind: ObservationAccessLogFailure, Component: "access_logger",
				Route: state.Route, Upstream: state.Upstream, Status: state.Status, Elapsed: state.Elapsed,
			})
		}
	}
	gateway.safeObserve(background, Observation{
		Kind:  ObservationRequestCompleted,
		Route: state.Route, Upstream: state.Upstream, Status: state.Status, Elapsed: state.Elapsed,
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

func callAccessLogger(ctx context.Context, logger AccessLogger, accessLog AccessLog) (err error) {
	defer func() {
		if recover() != nil {
			err = errors.New("gateway access logger panicked")
		}
	}()
	return logger.Log(ctx, accessLog)
}

func recordGatewayError(r *http.Request, gatewayError GatewayError) {
	state := accessLogStateFromContext(r.Context())
	if state == nil {
		return
	}
	state.ErrorCode = gatewayError.Code
	if gatewayError.Cause != nil {
		state.FailureComponent = gatewayError.Code
	}
}

func setAuthResult(r *http.Request, result string, identity *Identity) {
	state := accessLogStateFromContext(r.Context())
	if state == nil {
		return
	}
	state.AuthResult = result
	if identity != nil {
		state.UserID = identity.UserID
		state.Roles = append([]string(nil), identity.Roles...)
	}
}

func setRateLimitResult(r *http.Request, scope RateLimitScope, result string) {
	state := accessLogStateFromContext(r.Context())
	if state != nil {
		state.RateLimitResult[scope] = result
	}
}

func accessLogStateFromContext(ctx context.Context) *accessLogState {
	state, _ := ctx.Value(accessLogStateContextKey).(*accessLogState)
	return state
}

func cloneAccessLog(accessLog AccessLog) AccessLog {
	accessLog.Roles = append([]string(nil), accessLog.Roles...)
	// 旁路日志可以持有自己的副本；先复制原 map，不能覆盖后再遍历。
	accessLog.RateLimitResult = maps.Clone(accessLog.RateLimitResult)
	return accessLog
}

func defaultErrorCode(status int) string {
	switch {
	case status == http.StatusRequestEntityTooLarge:
		return "request_body_too_large"
	case status == http.StatusBadGateway:
		return "upstream_unavailable"
	case status == http.StatusGatewayTimeout:
		return "upstream_timeout"
	case status >= http.StatusInternalServerError:
		return "upstream_error"
	default:
		return ""
	}
}

func isNilInterface(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
