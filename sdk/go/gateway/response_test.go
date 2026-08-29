package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type envelopeSnapshot struct {
	Code        int             `json:"code"`
	Message     string          `json:"message"`
	Data        json.RawMessage `json:"data"`
	Timestamp   string          `json:"timestamp"`
	ErrorReason string          `json:"error_reason"`
}

func TestHealthSuccessEnvelopeRemainsCompatible(t *testing.T) {
	handler, err := New(
		WithRoutes(publicRoute()),
		withTestUpstreams(map[string]http.Handler{"backend": http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})}),
		WithHealth(HealthConfig{Service: "example-gateway"}),
	)
	if err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://gateway/health/live", nil))
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("status = %d, content type = %q", response.Code, response.Header().Get("Content-Type"))
	}
	var envelope envelopeSnapshot
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Code != http.StatusOK || envelope.Message != "操作成功" || envelope.ErrorReason != "" {
		t.Fatalf("envelope = %#v", envelope)
	}
	var data map[string]string
	if err := json.Unmarshal(envelope.Data, &data); err != nil {
		t.Fatal(err)
	}
	if data["status"] != "ok" || data["service"] != "example-gateway" {
		t.Fatalf("data = %#v", data)
	}
	assertEnvelopeTimestamp(t, envelope.Timestamp)
}

func TestDefaultErrorEnvelopeRemainsCompatible(t *testing.T) {
	handler, err := New(
		WithRoutes(publicRoute()),
		withTestUpstreams(map[string]http.Handler{"backend": http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})}),
	)
	if err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://gateway/missing", nil))
	if response.Code != http.StatusNotFound || response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("status = %d, content type = %q", response.Code, response.Header().Get("Content-Type"))
	}
	var envelope envelopeSnapshot
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Code != http.StatusNotFound || envelope.Message != "route not found" || envelope.ErrorReason != "route_not_found" {
		t.Fatalf("envelope = %#v", envelope)
	}
	if string(envelope.Data) != "null" {
		t.Fatalf("data = %s", envelope.Data)
	}
	assertEnvelopeTimestamp(t, envelope.Timestamp)
}

func assertEnvelopeTimestamp(t *testing.T, value string) {
	t.Helper()
	if _, err := time.Parse(time.RFC3339Nano, value); err != nil {
		t.Fatalf("timestamp = %q: %v", value, err)
	}
}
