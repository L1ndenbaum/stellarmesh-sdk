// Package api 包含共享 HTTP JSON 契约和辅助函数。
package api

// Envelope 是 Stellarmesh 服务共享的响应结构。
type Envelope struct {
	Code        int    `json:"code"`
	Message     string `json:"message"`
	Data        any    `json:"data"`
	Timestamp   string `json:"timestamp"`
	ErrorReason string `json:"error_reason,omitempty"`
}
