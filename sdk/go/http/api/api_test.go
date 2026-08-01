package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWriteJSONUsesEnvelope(t *testing.T) {
	recorder := httptest.NewRecorder()
	WriteJSON(recorder, http.StatusAccepted, map[string]int{"accepted": 1})

	var envelope Envelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Code != http.StatusAccepted || envelope.Data == nil {
		t.Fatalf("envelope = %#v", envelope)
	}
}

func TestDecodeJSONRejectsTrailingValues(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"one"}{"name":"two"}`))
	var target struct {
		Name string `json:"name"`
	}
	if DecodeJSON(recorder, request, &target, DefaultJSONBodyLimit) {
		t.Fatal("DecodeJSON() accepted trailing JSON")
	}
}

func TestTokenAuthRequiresConfiguredToken(t *testing.T) {
	handler := TokenAuth(TokenAuthConfig{Header: "X-Token"})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", recorder.Code)
	}
}
