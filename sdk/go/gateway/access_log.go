package gateway

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"time"

	sharedlogging "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging"
)

var errAccessLogRejected = errors.New("gateway access log emitter rejected event")

type accessLogState struct {
	AccessLog
	StartedAt        time.Time
	FailureComponent string
	SkipSuccessful   bool
}

type emitterAccessLogger struct {
	service string
	emitter sharedlogging.Emitter
}

// WithAccessLogEmitter 把网关访问日志直接适配到已有异步日志客户端。
func WithAccessLogEmitter(service string, emitter sharedlogging.Emitter) Option {
	return componentOption("access_logger", func(config *config) error {
		service = strings.TrimSpace(service)
		if service == "" {
			return errors.New("gateway access log service is required")
		}
		if isNilInterface(emitter) {
			return errors.New("gateway access log emitter is nil")
		}
		config.accessLogger = &emitterAccessLogger{service: service, emitter: emitter}
		return nil
	})
}

func (logger *emitterAccessLogger) Log(ctx context.Context, accessLog AccessLog) error {
	level := sharedlogging.LevelInfo
	if accessLog.Status >= http.StatusInternalServerError {
		level = sharedlogging.LevelError
	} else if accessLog.Status >= http.StatusBadRequest {
		level = sharedlogging.LevelWarning
	}
	metadata := map[string]any{
		"request_id":           accessLog.RequestID,
		"method":               accessLog.Method,
		"path":                 accessLog.Path,
		"route":                accessLog.Route,
		"client_ip":            accessLog.ClientIP,
		"auth_result":          accessLog.AuthResult,
		"user_id":              accessLog.UserID,
		"roles":                append([]string(nil), accessLog.Roles...),
		"upstream":             accessLog.Upstream,
		"status":               accessLog.Status,
		"elapsed_milliseconds": accessLog.Elapsed.Milliseconds(),
		"error_code":           accessLog.ErrorCode,
	}
	rateResults := make(map[string]string, len(accessLog.RateLimitResult))
	for scope, result := range accessLog.RateLimitResult {
		rateResults[string(scope)] = result
	}
	metadata["rate_limit_result"] = rateResults
	accepted := logger.emitter.Emit(ctx, sharedlogging.Event{
		Timestamp: accessLog.Timestamp.UTC(),
		Level:     level,
		Service:   logger.service,
		Message:   "gateway request",
		TraceID:   accessLog.RequestID,
		Metadata:  sharedlogging.SanitizeMetadata(metadata),
	})
	if !accepted {
		return errAccessLogRejected
	}
	return nil
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
	accessLog.RateLimitResult = make(map[RateLimitScope]string, len(accessLog.RateLimitResult))
	for scope, result := range accessLog.RateLimitResult {
		accessLog.RateLimitResult[scope] = result
	}
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
