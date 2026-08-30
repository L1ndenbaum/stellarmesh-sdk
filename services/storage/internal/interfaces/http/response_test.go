package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestResponseKeepsStorageEnvelopeContract(t *testing.T) {
	recorder := httptest.NewRecorder()
	writeError(recorder, http.StatusForbidden, "storage access forbidden")

	var payload envelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusForbidden || payload.Code != http.StatusForbidden || payload.Message != "storage access forbidden" || payload.Data != nil {
		t.Fatalf("response = %#v", payload)
	}
	if _, err := time.Parse(time.RFC3339Nano, payload.Timestamp); err != nil {
		t.Fatalf("timestamp = %q", payload.Timestamp)
	}
}
