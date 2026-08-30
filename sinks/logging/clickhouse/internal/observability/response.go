package observability

import (
	"encoding/json"
	"net/http"
	"time"
)

type envelope struct {
	Code        int    `json:"code"`
	Message     string `json:"message"`
	Data        any    `json:"data"`
	Timestamp   string `json:"timestamp"`
	ErrorReason string `json:"error_reason,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	message := "操作成功"
	if status >= http.StatusBadRequest {
		message = "请求失败"
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(envelope{
		Code: status, Message: message, Data: data,
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
	})
}
