package httpapi

import "net/http"

// HandleHealth 报告进程存活。
func (handler *Handler) HandleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// HandleReady 读取最近的周期检查结果，不在请求中实时探测 S3。
// 缺少 readiness 组件也必须返回未就绪，不能以进程存活代替依赖可用。
func (handler *Handler) HandleReady(w http.ResponseWriter, _ *http.Request) {
	if handler.readiness == nil || !handler.readiness.Ready() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}
