// Package infrastructure writes canonical events to ClickHouse.
package infrastructure

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	sharedlogging "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging"
)

const tableName = "log_events"

// WriterConfig configures ClickHouse HTTP JSONEachRow insertion.
type WriterConfig struct {
	BaseURL  string
	Database string
	Username string
	Password string
	Timeout  time.Duration
	Client   *http.Client
	Now      func() time.Time
}

// Writer inserts canonical events into the fixed log_events table.
type Writer struct {
	baseURL  string
	database string
	username string
	password string
	client   *http.Client
	now      func() time.Time
}

// NewWriter creates a ClickHouse HTTP writer.
func NewWriter(config WriterConfig) *Writer {
	timeout := config.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	client := config.Client
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	now := config.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Writer{
		baseURL: strings.TrimRight(config.BaseURL, "/"), database: config.Database,
		username: config.Username, password: config.Password, client: client, now: now,
	}
}

// InsertEvents inserts one JSONEachRow batch.
func (writer *Writer) InsertEvents(ctx context.Context, events []sharedlogging.Event) error {
	if len(events) == 0 {
		return nil
	}
	var body bytes.Buffer
	buffered := bufio.NewWriter(&body)
	encoder := json.NewEncoder(buffered)
	for _, event := range events {
		metadata, err := json.Marshal(event.Metadata)
		if err != nil {
			return err
		}
		if err := encoder.Encode(logRow{
			EventID: event.EventID, Timestamp: formatTime(event.Timestamp), Level: string(event.Level),
			Service: event.Service, Message: event.Message, TraceID: event.TraceID,
			MetadataJSON: string(metadata), IngestedAt: formatTime(writer.now()),
		}); err != nil {
			return err
		}
	}
	if err := buffered.Flush(); err != nil {
		return err
	}
	requestURL, err := writer.insertURL()
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, &body)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	if writer.username != "" {
		request.SetBasicAuth(writer.username, writer.password)
	}
	response, err := writer.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("clickhouse insert returned %d", response.StatusCode)
	}
	return nil
}

func (writer *Writer) insertURL() (string, error) {
	parsed, err := url.Parse(writer.baseURL)
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	query.Set("database", writer.database)
	query.Set("query", "INSERT INTO "+tableName+" FORMAT JSONEachRow")
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func formatTime(value time.Time) string {
	return value.UTC().Format("2006-01-02 15:04:05.000")
}

type logRow struct {
	EventID      string `json:"event_id"`
	Timestamp    string `json:"timestamp"`
	Level        string `json:"level"`
	Service      string `json:"service"`
	Message      string `json:"message"`
	TraceID      string `json:"trace_id"`
	MetadataJSON string `json:"metadata_json"`
	IngestedAt   string `json:"ingested_at"`
}
