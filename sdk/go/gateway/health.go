package gateway

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
)

// ReadinessChecker 检查网关当前是否可以可靠接收流量。
type ReadinessChecker interface {
	Check(context.Context) error
}

// ReadinessCheckerFunc 让函数直接实现 ReadinessChecker。
type ReadinessCheckerFunc func(context.Context) error

// HealthConfig 配置网关本地存活和就绪端点。
type HealthConfig struct {
	// Service 留空使用 gateway，只用于响应标识。
	Service string
	// LivePath 留空使用 /health/live；必须与 ReadyPath 不同且以 / 开头。
	LivePath string
	// ReadyPath 留空使用 /health/ready。
	ReadyPath string
	// CheckTimeout 为 0 时使用 2 秒；负数拒绝，检查器应响应 context 取消。
	CheckTimeout time.Duration
	// Readiness 可省略；配置后失败返回 503，不自动探测未声明的依赖。
	Readiness ReadinessChecker
	// LogSuccessful 默认 false；健康检查失败仍产生访问日志。
	LogSuccessful bool
	// Responder 省略时使用默认文本响应；自定义只负责成功响应。
	Responder HealthResponder
}

// HealthKind 标识通过检查的健康端点类型。
type HealthKind string

const (
	// HealthKindLive 表示进程存活检查。
	HealthKindLive HealthKind = "live"
	// HealthKindReady 表示流量就绪检查。
	HealthKindReady HealthKind = "ready"
)

// HealthResult 描述已经通过检查的健康端点。
type HealthResult struct {
	Kind    HealthKind
	Service string
}

// HealthResponder 编码健康检查成功响应；失败响应仍由 ErrorResponder 处理。
type HealthResponder interface {
	RespondHealth(http.ResponseWriter, *http.Request, HealthResult)
}

// HealthResponderFunc 让函数直接实现 HealthResponder。
type HealthResponderFunc func(http.ResponseWriter, *http.Request, HealthResult)

const defaultReadinessTimeout = 2 * time.Second

type healthPolicy struct {
	service       string
	livePath      string
	readyPath     string
	checkTimeout  time.Duration
	readiness     ReadinessChecker
	logSuccessful bool
	responder     HealthResponder
}

type defaultHealthResponder struct{}

// Check 调用就绪检查函数。
func (checker ReadinessCheckerFunc) Check(ctx context.Context) error {
	return checker(ctx)
}

// RespondHealth 调用健康响应函数。
func (responder HealthResponderFunc) RespondHealth(w http.ResponseWriter, r *http.Request, result HealthResult) {
	responder(w, r, result)
}

// WithHealth 启用网关本地存活和就绪端点。
func WithHealth(health HealthConfig) Option {
	return componentOption("health", func(config *config) error {
		copied := health
		config.health = &copied
		return nil
	})
}

func newHealthPolicy(config *HealthConfig) (*healthPolicy, error) {
	if config == nil {
		return nil, nil
	}
	service := strings.TrimSpace(config.Service)
	if service == "" {
		service = "gateway"
	}
	livePath := strings.TrimSpace(config.LivePath)
	if livePath == "" {
		livePath = "/health/live"
	}
	readyPath := strings.TrimSpace(config.ReadyPath)
	if readyPath == "" {
		readyPath = "/health/ready"
	}
	if !strings.HasPrefix(livePath, "/") || !strings.HasPrefix(readyPath, "/") || livePath == readyPath {
		return nil, errors.New("gateway health paths must be distinct absolute paths")
	}
	checkTimeout := config.CheckTimeout
	if checkTimeout == 0 {
		checkTimeout = defaultReadinessTimeout
	}
	if checkTimeout < 0 {
		return nil, errors.New("gateway readiness timeout cannot be negative")
	}
	readiness := config.Readiness
	if isNilInterface(readiness) {
		readiness = nil
	}
	responder := config.Responder
	if responder == nil {
		responder = defaultHealthResponder{}
	} else if isNilInterface(responder) {
		return nil, errors.New("gateway health responder is nil")
	}
	return &healthPolicy{
		service: service, livePath: livePath, readyPath: readyPath,
		checkTimeout: checkTimeout, readiness: readiness, logSuccessful: config.LogSuccessful,
		responder: responder,
	}, nil
}

func (policy *healthPolicy) handle(w http.ResponseWriter, r *http.Request, gateway *Gateway, state *accessLogState) bool {
	if r.URL.Path != policy.livePath && r.URL.Path != policy.readyPath {
		return false
	}
	state.AuthResult = "public"
	state.Upstream = ""
	if r.URL.Path == policy.livePath {
		state.Route = "health_live"
	} else {
		state.Route = "health_ready"
	}
	if r.Method != http.MethodGet {
		gateway.fail(w, r, GatewayError{Status: http.StatusMethodNotAllowed, Code: "health_method_not_allowed", Message: "method not allowed"})
		return true
	}
	if r.URL.Path == policy.readyPath && policy.readiness != nil {
		ctx, cancel := context.WithTimeout(r.Context(), policy.checkTimeout)
		err := policy.readiness.Check(ctx)
		cancel()
		if err != nil {
			gateway.fail(w, r, unavailableError("readiness_failed", err))
			return true
		}
	}
	state.SkipSuccessful = !policy.logSuccessful
	kind := HealthKindLive
	if r.URL.Path == policy.readyPath {
		kind = HealthKindReady
	}
	policy.responder.RespondHealth(w, r, HealthResult{Kind: kind, Service: policy.service})
	return true
}

func (defaultHealthResponder) RespondHealth(w http.ResponseWriter, _ *http.Request, _ HealthResult) {
	writePlainText(w, http.StatusOK, "ok")
}
