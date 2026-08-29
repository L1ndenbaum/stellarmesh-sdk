package gateway

import (
	"encoding/json"
	"net/http"
	"time"
)

const (
	headerXRequestID = "X-Request-ID"
	headerXUserID    = "X-User-ID"
	headerXUserRoles = "X-User-Roles"
)

// apiEnvelope 保持 Gateway 已发布的 HTTP 响应结构，同时避免反向依赖父 SDK。
type apiEnvelope struct {
	Code        int    `json:"code"`
	Message     string `json:"message"`
	Data        any    `json:"data"`
	Timestamp   string `json:"timestamp"`
	ErrorReason string `json:"error_reason,omitempty"`
}

func writeSuccessJSON(w http.ResponseWriter, status int, payload any) {
	writeEnvelope(w, status, apiEnvelope{
		Code:      status,
		Message:   "操作成功",
		Data:      payload,
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
	})
}

func writeEnvelope(w http.ResponseWriter, status int, envelope apiEnvelope) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(envelope)
}
