package api

import (
	"encoding/json"
	"net/http"
	"time"
)

// WriteJSON 使用 Envelope 写入成功响应或调用方提供的响应。
func WriteJSON(w http.ResponseWriter, status int, payload any) {
	message := "操作成功"
	if status >= http.StatusBadRequest {
		message = "请求失败"
	}
	writeEnvelope(w, status, Envelope{
		Code:      status,
		Message:   message,
		Data:      payload,
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
	})
}

// WriteError 写入错误 Envelope。
func WriteError(w http.ResponseWriter, status int, message string) {
	writeEnvelope(w, status, Envelope{
		Code:      status,
		Message:   message,
		Data:      nil,
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
	})
}

func writeEnvelope(w http.ResponseWriter, status int, payload Envelope) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
