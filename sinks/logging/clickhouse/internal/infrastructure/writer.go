// Package infrastructure 将规范事件写入 ClickHouse。
package infrastructure

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	sharedlogging "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging"
)

const tableName = "log_events"

// WriterConfig 配置 ClickHouse HTTP JSONEachRow 写入。
type WriterConfig struct {
	BaseURL  string
	Database string
	Username string
	Password string
	Timeout  time.Duration
	Client   *http.Client
	Now      func() time.Time
}

// Writer 将规范事件写入固定的 log_events 表。
type Writer struct {
	baseURL  string
	database string
	username string
	password string
	client   *http.Client
	now      func() time.Time
}

// NewWriter 创建 ClickHouse HTTP writer。
func NewWriter(config WriterConfig) (*Writer, error) {
	parsed, err := url.Parse(strings.TrimSpace(config.BaseURL))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, errors.New("ClickHouse base URL must be an absolute HTTP URL")
	}
	if parsed.User != nil {
		return nil, errors.New("ClickHouse credentials must not be embedded in the base URL")
	}
	if strings.TrimSpace(config.Database) == "" || strings.TrimSpace(config.Username) == "" {
		return nil, errors.New("ClickHouse database and runtime user are required")
	}
	if config.Timeout < 0 {
		return nil, errors.New("ClickHouse HTTP timeout must not be negative")
	}
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
		baseURL: strings.TrimRight(strings.TrimSpace(config.BaseURL), "/"), database: strings.TrimSpace(config.Database),
		username: strings.TrimSpace(config.Username), password: config.Password, client: client, now: now,
	}, nil
}

// Check 在无需 DDL 权限的情况下校验 ClickHouse 连通性和运行时凭据。
func (writer *Writer) Check(ctx context.Context) error {
	requestURL, err := writer.queryURL("SELECT 1")
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return err
	}
	request.SetBasicAuth(writer.username, writer.password)
	response, err := writer.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return clickHouseResponseError(response)
	}
	_, err = io.Copy(io.Discard, response.Body)
	return err
}

// InsertEvents 写入一个 JSONEachRow 批次。
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
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return clickHouseResponseError(response)
	}
	_, err = io.Copy(io.Discard, response.Body)
	return err
}

func (writer *Writer) insertURL() (string, error) {
	return writer.queryURL("INSERT INTO " + tableName + " FORMAT JSONEachRow")
}

func (writer *Writer) queryURL(queryText string) (string, error) {
	parsed, err := url.Parse(writer.baseURL)
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	query.Set("database", writer.database)
	query.Set("query", queryText)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func clickHouseResponseError(response *http.Response) error {
	payload, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
	detail := strings.TrimSpace(string(payload))
	if detail == "" {
		return fmt.Errorf("ClickHouse returned HTTP %d", response.StatusCode)
	}
	return fmt.Errorf("ClickHouse returned HTTP %d: %s", response.StatusCode, detail)
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
