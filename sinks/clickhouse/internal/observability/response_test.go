package observability

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestResponseKeepsSinkEnvelopeContract(t *testing.T) {
	recorder := httptest.NewRecorder()
	writeJSON(recorder, http.StatusServiceUnavailable, map[string]string{"status": "not_ready"})

	var payload envelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusServiceUnavailable || payload.Code != http.StatusServiceUnavailable || payload.Message != "请求失败" || payload.Data == nil {
		t.Fatalf("response = %#v", payload)
	}
	if _, err := time.Parse(time.RFC3339Nano, payload.Timestamp); err != nil {
		t.Fatalf("timestamp = %q", payload.Timestamp)
	}
}
