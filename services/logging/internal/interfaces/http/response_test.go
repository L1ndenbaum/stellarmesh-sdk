package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestResponseKeepsLoggingEnvelopeContract(t *testing.T) {
	recorder := httptest.NewRecorder()
	writeJSON(recorder, http.StatusAccepted, map[string]int{"accepted": 1})

	var payload envelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusAccepted || payload.Code != http.StatusAccepted || payload.Message != "操作成功" || payload.Data == nil {
		t.Fatalf("response = %#v", payload)
	}
	if _, err := time.Parse(time.RFC3339Nano, payload.Timestamp); err != nil {
		t.Fatalf("timestamp = %q", payload.Timestamp)
	}
}
