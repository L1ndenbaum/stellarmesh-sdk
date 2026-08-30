package infrastructure

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	sharedlogging "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestWriterUsesFixedTableAndJSONEachRow(t *testing.T) {
	var query string
	var body string
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		query = request.URL.Query().Get("query")
		payload, err := io.ReadAll(request.Body)
		if err != nil {
			return nil, err
		}
		body = string(payload)
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	id, err := sharedlogging.NewEventID()
	if err != nil {
		t.Fatal(err)
	}
	writer, err := NewWriter(WriterConfig{
		BaseURL: "http://clickhouse:8123", Database: "logging_db", Username: "runtime",
		Client: client, Now: func() time.Time { return time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	event := sharedlogging.Event{
		EventID: id, Timestamp: time.Now(), Kind: sharedlogging.EventKindAudit, Level: sharedlogging.LevelInfo,
		Service: "test", Message: "event", Metadata: map[string]any{"safe": true},
	}
	if err := writer.InsertEvents(context.Background(), []sharedlogging.Event{event}); err != nil {
		t.Fatal(err)
	}
	if query != "INSERT INTO log_events FORMAT JSONEachRow" || !strings.Contains(body, `"kind":"AUDIT"`) ||
		!strings.Contains(body, `"level":"INFO"`) || !strings.Contains(body, `"metadata_json":"{\"safe\":true}"`) {
		t.Fatalf("query=%q body=%s", query, body)
	}
}

func TestWriterChecksConnectivityAndCredentials(t *testing.T) {
	var query string
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		query = request.URL.Query().Get("query")
		username, password, ok := request.BasicAuth()
		if !ok || username != "runtime" || password != "secret" {
			t.Fatalf("basic auth = %q %q %v", username, password, ok)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("1\n"))}, nil
	})}
	writer, err := NewWriter(WriterConfig{
		BaseURL: "http://clickhouse:8123", Database: "logging_db", Username: "runtime",
		Password: "secret", Client: client,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if query != "SELECT 1" {
		t.Fatalf("query = %q", query)
	}
}

func TestNewWriterRejectsInvalidConfiguration(t *testing.T) {
	if _, err := NewWriter(WriterConfig{BaseURL: "://invalid", Database: "logging", Username: "runtime"}); err == nil {
		t.Fatal("NewWriter() accepted invalid URL")
	}
	if _, err := NewWriter(WriterConfig{BaseURL: "http://clickhouse:8123", Database: "logging"}); err == nil {
		t.Fatal("NewWriter() accepted empty runtime user")
	}
	if _, err := NewWriter(WriterConfig{BaseURL: "http://user:secret@clickhouse:8123", Database: "logging", Username: "runtime"}); err == nil {
		t.Fatal("NewWriter() accepted credentials in URL")
	}
}
