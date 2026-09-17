package gateway

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"
)

// GatewayError 描述网关确定的 HTTP 错误语义。
// Cause 只供内部诊断，响应器不得把它直接返回给客户端。
type GatewayError struct {
	Status     int
	Code       string
	Message    string
	RetryAfter time.Duration
	Cause      error
}

// ErrorResponder 把网关错误写入 HTTP 响应。
type ErrorResponder interface {
	Respond(http.ResponseWriter, *http.Request, GatewayError)
}

// ErrorResponderFunc 让函数直接实现 ErrorResponder。
type ErrorResponderFunc func(http.ResponseWriter, *http.Request, GatewayError)

type protocolErrorResponder struct {
	next ErrorResponder
}

type defaultErrorResponder struct{}

// Respond 调用错误响应函数。
func (responder ErrorResponderFunc) Respond(w http.ResponseWriter, r *http.Request, gatewayError GatewayError) {
	responder(w, r, gatewayError)
}

// WithErrorResponder 使用项目定义的错误响应格式。
func WithErrorResponder(responder ErrorResponder) Option {
	return componentOption("error_responder", func(config *config) error {
		if isNilInterface(responder) {
			return errors.New("gateway error responder is nil")
		}
		config.errorResponder = responder
		return nil
	})
}

func (responder protocolErrorResponder) Respond(w http.ResponseWriter, r *http.Request, gatewayError GatewayError) {
	if gatewayError.RetryAfter > 0 {
		seconds := int64(gatewayError.RetryAfter / time.Second)
		if gatewayError.RetryAfter%time.Second != 0 {
			seconds++
		}
		w.Header().Set("Retry-After", strconv.FormatInt(seconds, 10))
	}
	defer func() {
		if recover() == nil || responseAlreadyStarted(w) {
			return
		}
		// 项目响应器属于不可信扩展点，异常时用中立响应兜底，避免重复触发同一响应器。
		fallback := GatewayError{
			Status: http.StatusInternalServerError, Code: "gateway_panic",
			Message: "internal server error",
		}
		recordGatewayError(r, fallback)
		defaultErrorResponder{}.Respond(w, r, fallback)
	}()
	responder.next.Respond(w, r, gatewayError)
}

func responseAlreadyStarted(w http.ResponseWriter) bool {
	type responseState interface {
		WroteHeader() bool
	}
	state, ok := w.(responseState)
	return ok && state.WroteHeader()
}

func (defaultErrorResponder) Respond(w http.ResponseWriter, _ *http.Request, gatewayError GatewayError) {
	writePlainText(w, gatewayError.Status, gatewayError.Message)
}

func writePlainText(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, message+"\n")
}

func (gateway *Gateway) fail(w http.ResponseWriter, r *http.Request, gatewayError GatewayError) {
	recordGatewayError(r, gatewayError)
	gateway.errorResponder.Respond(w, r, gatewayError)
}

func unavailableError(code string, cause error) GatewayError {
	return GatewayError{Status: http.StatusServiceUnavailable, Code: code, Message: "service unavailable", Cause: cause}
}
