package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestContractFixture(t *testing.T) {
	payload, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "logging", "v1", "testdata", "valid-event.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeEvent(payload); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidContractFixtures(t *testing.T) {
	payload, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "logging", "v1", "testdata", "invalid-events.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Name    string          `json:"name"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(payload, &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			if _, err := DecodeEvent(fixture.Payload); err == nil {
				t.Fatal("DecodeEvent() accepted invalid fixture")
			}
		})
	}
}

func TestSanitizeMetadata(t *testing.T) {
	type credentials struct {
		Password string `json:"password"`
		Safe     string `json:"safe"`
	}
	got := SanitizeMetadata(map[string]any{
		"api_token": "secret",
		"nested":    map[string]string{"password": "hidden", "safe": "value"},
		"typed":     credentials{Password: "hidden", Safe: "value"},
		"channel":   make(chan int),
	})
	if got["api_token"] != redactedValue {
		t.Fatalf("api_token = %v", got["api_token"])
	}
	if got["nested"].(map[string]any)["password"] != redactedValue {
		t.Fatalf("nested = %#v", got["nested"])
	}
	if got["typed"].(map[string]any)["password"] != redactedValue {
		t.Fatalf("typed = %#v", got["typed"])
	}
	if got["channel"] != unserializableValue {
		t.Fatalf("channel = %#v", got["channel"])
	}
}

func TestSanitizeMetadataTruncatesUnicodeByRune(t *testing.T) {
	value := strings.Repeat("界", maxStringLength+1)
	got := SanitizeMetadata(map[string]any{"value": value})["value"].(string)
	if !strings.HasSuffix(got, truncatedValueLabel) || len([]rune(strings.TrimSuffix(got, truncatedValueLabel))) != maxStringLength {
		t.Fatalf("unexpected truncated value length=%d", len([]rune(got)))
	}
}

func TestClientBatchesAndDrains(t *testing.T) {
	var mu sync.Mutex
	var messages []string
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("X-Logging-Service-Token") != "token" {
			t.Error("missing token")
		}
		var request BatchIngestRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		mu.Lock()
		for _, event := range request.Events {
			messages = append(messages, event.Message)
		}
		mu.Unlock()
		payload, err := json.Marshal(map[string]any{
			"code": http.StatusAccepted, "message": "ok", "data": map[string]int{"accepted": len(request.Events)},
			"timestamp": "2026-08-01T12:00:00Z",
		})
		if err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: http.StatusAccepted,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewReader(payload)),
		}, nil
	})}

	client, err := NewClient(ClientConfig{BaseURL: "http://logging-service", Token: "token", BatchSize: 2, FlushInterval: time.Hour, HTTPClient: httpClient})
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range []string{"first", "second"} {
		if !client.Emit(context.Background(), Event{Level: LevelInfo, Service: "test", Message: message}) {
			t.Fatalf("Emit(%q) = false", message)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.Close(ctx); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(messages) != 2 || messages[0] != "first" || messages[1] != "second" {
		t.Fatalf("messages = %#v", messages)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestClientReportsQueueFailure(t *testing.T) {
	dropped := make(chan error, 1)
	client, err := NewClient(ClientConfig{BaseURL: "http://logging-service", Token: "token", QueueSize: 1, BatchSize: 1, HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("unavailable")
	})}, OnDrop: func(_ Event, err error) {
		select {
		case dropped <- err:
		default:
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !client.Emit(context.Background(), Event{Level: LevelInfo, Service: "test", Message: "event"}) {
		t.Fatal("Emit() = false")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.Close(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-dropped:
	default:
		t.Fatal("drop handler was not called")
	}
}

func TestClientAcceptsLegacyOKResponse(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		payload := `{"code":200,"message":"ok","data":{"accepted":1},"timestamp":"2026-08-01T12:00:00Z"}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(payload))}, nil
	})}
	client, err := NewClient(ClientConfig{BaseURL: "http://logging-service", Token: "token", BatchSize: 1, HTTPClient: httpClient})
	if err != nil {
		t.Fatal(err)
	}
	if !client.Emit(context.Background(), Event{Level: LevelInfo, Service: "test", Message: "event"}) {
		t.Fatal("Emit() = false")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestClientIsolatesDropHandlerPanic(t *testing.T) {
	var fallback bytes.Buffer
	client, err := NewClient(ClientConfig{
		BaseURL: "http://logging-service", Token: "token", BatchSize: 1,
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("unavailable")
		})},
		OnDrop: func(Event, error) { panic("callback failed") }, FallbackWriter: &fallback,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !client.Emit(context.Background(), Event{Level: LevelInfo, Service: "test", Message: "event"}) {
		t.Fatal("Emit() = false")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fallback.String(), "drop handler panicked") {
		t.Fatalf("fallback = %q", fallback.String())
	}
}

func TestClientAndLoggerRejectInvalidConfiguration(t *testing.T) {
	if _, err := NewClient(ClientConfig{BaseURL: "://invalid", Token: "token"}); err == nil {
		t.Fatal("NewClient() accepted invalid URL")
	}
	if _, err := NewClient(ClientConfig{BaseURL: "http://logging-service"}); err == nil {
		t.Fatal("NewClient() accepted empty token")
	}
	if _, err := NewLogger(LoggerConfig{}); err == nil {
		t.Fatal("NewLogger() accepted invalid configuration")
	}
}
