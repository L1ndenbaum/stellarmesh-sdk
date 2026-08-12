// Package headers 定义可信服务间 HTTP 请求头。
package headers

import (
	"net"
	"net/http"
	"strings"
)

const (
	HeaderXRequestID    = "X-Request-ID"
	HeaderXUserID       = "X-User-ID"
	HeaderXUserRoles    = "X-User-Roles"
	HeaderXForwardedFor = "X-Forwarded-For"
	HeaderXRealIP       = "X-Real-IP"
)

// ClientIP 从可信代理请求头或 RemoteAddr 中提取调用方 IP。
func ClientIP(r *http.Request) string {
	if forwardedFor := strings.TrimSpace(r.Header.Get(HeaderXForwardedFor)); forwardedFor != "" {
		parts := strings.Split(forwardedFor, ",")
		return strings.TrimSpace(parts[0])
	}
	if realIP := strings.TrimSpace(r.Header.Get(HeaderXRealIP)); realIP != "" {
		return realIP
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}
