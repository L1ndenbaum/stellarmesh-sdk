package gateway

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"time"
)

// AccessLog 是不包含查询参数或凭据的网关访问记录。
type AccessLog struct {
	Timestamp       time.Time
	RequestID       string
	Method          string
	Path            string
	Route           string
	ClientIP        string
	AuthResult      string
	UserID          string
	Roles           []string
	Upstream        string
	Status          int
	Elapsed         time.Duration
	ErrorCode       string
	RateLimitResult map[RateLimitScope]string
}

// AccessLogger 接收请求完成后的规范访问记录。
type AccessLogger interface {
	Log(context.Context, AccessLog) error
}

// AccessLoggerFunc 让函数直接实现 AccessLogger。
type AccessLoggerFunc func(context.Context, AccessLog) error

type accessLogState struct {
	AccessLog
	StartedAt        time.Time
	FailureComponent string
	SkipSuccessful   bool
}

// Log 调用访问日志函数。
func (logger AccessLoggerFunc) Log(ctx context.Context, accessLog AccessLog) error {
	return logger(ctx, accessLog)
}

// WithAccessLogger 启用请求完成后的旁路访问日志。
func WithAccessLogger(logger AccessLogger) Option {
	return componentOption("access_logger", func(config *config) error {
		if isNilInterface(logger) {
			return errors.New("gateway access logger is nil")
		}
		config.accessLogger = logger
		return nil
	})
}

// WithoutAccessLog 显式关闭默认启用的访问日志。
func WithoutAccessLog() Option {
	return componentOption("access_logger", func(config *config) error {
		config.accessLogDisabled = true
		return nil
	})
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
