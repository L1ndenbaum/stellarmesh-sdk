package gateway

import "net/http"

type responseRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func newResponseRecorder(w http.ResponseWriter) *responseRecorder {
	return &responseRecorder{ResponseWriter: w, status: http.StatusOK}
}

func (recorder *responseRecorder) WriteHeader(status int) {
	if recorder.wroteHeader {
		return
	}
	recorder.status = status
	recorder.wroteHeader = true
	recorder.ResponseWriter.WriteHeader(status)
}

func (recorder *responseRecorder) Write(body []byte) (int, error) {
	if !recorder.wroteHeader {
		recorder.WriteHeader(http.StatusOK)
	}
	return recorder.ResponseWriter.Write(body)
}

// Unwrap 让 ResponseController 继续访问底层 Flush、Hijack 和 deadline 能力。
func (recorder *responseRecorder) Unwrap() http.ResponseWriter {
	return recorder.ResponseWriter
}

func (recorder *responseRecorder) Status() int {
	return recorder.status
}

func (recorder *responseRecorder) WroteHeader() bool {
	return recorder.wroteHeader
}
