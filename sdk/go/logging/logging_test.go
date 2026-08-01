package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestContractFixture(t *testing.T) {
	payload, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "logging", "v1", "testdata", "valid-event.json"))
	if err != nil {
		t.Fatal(err)
	}
	var event Event
	if err := json.Unmarshal(payload, &event); err != nil {
		t.Fatal(err)
	}
	if err := event.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestSanitizeMetadata(t *testing.T) {
	got := SanitizeMetadata(map[string]any{"api_token": "secret", "nested": map[string]any{"safe": "value"}})
	if got["api_token"] != redactedValue {
		t.Fatalf("api_token = %v", got["api_token"])
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

	client := NewClient(ClientConfig{BaseURL: "http://logging-service", Token: "token", BatchSize: 2, FlushInterval: time.Hour, HTTPClient: httpClient})
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
	client := NewClient(ClientConfig{BaseURL: "://invalid", QueueSize: 1, BatchSize: 1, OnDrop: func(_ Event, err error) {
		select {
		case dropped <- err:
		default:
		}
	}})
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
