package gateway

import (
	"io"
	"net/http"
	"strconv"
	"time"
)

const (
	headerXRequestID = "X-Request-ID"
	headerXUserID    = "X-User-ID"
	headerXUserRoles = "X-User-Roles"
)

type protocolErrorResponder struct {
	next ErrorResponder
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

type defaultErrorResponder struct{}

func (defaultErrorResponder) Respond(w http.ResponseWriter, _ *http.Request, gatewayError GatewayError) {
	writePlainText(w, gatewayError.Status, gatewayError.Message)
}

type defaultHealthResponder struct{}

func (defaultHealthResponder) RespondHealth(w http.ResponseWriter, _ *http.Request, _ HealthResult) {
	writePlainText(w, http.StatusOK, "ok")
}

func writePlainText(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, message+"\n")
}
