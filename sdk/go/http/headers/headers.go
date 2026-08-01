// Package headers defines trusted service-to-service HTTP headers.
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

// ClientIP extracts the caller IP from trusted proxy headers or RemoteAddr.
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
