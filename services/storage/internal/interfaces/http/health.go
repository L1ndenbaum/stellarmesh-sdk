package httpapi

import "net/http"

// HandleHealth 报告进程存活。
func (handler *Handler) HandleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// HandleReady 报告全部 namespace 是否可访问。
func (handler *Handler) HandleReady(w http.ResponseWriter, _ *http.Request) {
	if handler.readiness == nil || !handler.readiness.Ready() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}
