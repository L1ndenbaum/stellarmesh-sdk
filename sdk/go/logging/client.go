package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	sharedapi "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/http/api"
)

const (
	defaultClientTimeout       = 2 * time.Second
	defaultClientQueueSize     = 4096
	defaultClientBatchSize     = 128
	defaultClientFlushInterval = 100 * time.Millisecond
	defaultMaxBatchBodyBytes   = 900 * 1024
)

var (
	ErrClientClosed  = errors.New("logging client is closed")
	ErrQueueFull     = errors.New("logging client queue is full")
	ErrEventTooLarge = errors.New("logging event exceeds the client body limit")
)

// DropHandler observes events that could not be queued or delivered.
type DropHandler func(Event, error)

// ClientConfig controls asynchronous HTTP delivery.
type ClientConfig struct {
	BaseURL        string
	Token          string
	Timeout        time.Duration
	QueueSize      int
	BatchSize      int
	FlushInterval  time.Duration
	MaxBodyBytes   int
	HTTPClient     *http.Client
	OnDrop         DropHandler
	FallbackWriter io.Writer
}

// Client asynchronously delivers v1 log batches.
type Client struct {
	baseURL             string
	token               string
	timeout             time.Duration
	batchSize           int
	flushInterval       time.Duration
	maxBodyBytes        int
	httpClient          *http.Client
	onDrop              DropHandler
	fallbackWriter      io.Writer
	queue               chan Event
	done                chan struct{}
	mu                  sync.RWMutex
	closed              bool
	closeOnce           sync.Once
	fallbackMu          sync.Mutex
	lastFallbackWarning time.Time
}

// NewClient creates and starts an asynchronous logging client.
func NewClient(config ClientConfig) (*Client, error) {
	if err := validateClientConfig(config); err != nil {
		return nil, err
	}
	timeout := config.Timeout
	if timeout <= 0 {
		timeout = defaultClientTimeout
	}
	queueSize := config.QueueSize
	if queueSize <= 0 {
		queueSize = defaultClientQueueSize
	}
	batchSize := config.BatchSize
	if batchSize <= 0 {
		batchSize = defaultClientBatchSize
	}
	flushInterval := config.FlushInterval
	if flushInterval <= 0 {
		flushInterval = defaultClientFlushInterval
	}
	maxBodyBytes := config.MaxBodyBytes
	if maxBodyBytes <= 0 {
		maxBodyBytes = defaultMaxBatchBodyBytes
	}
	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: timeout}
	}
	client := &Client{
		baseURL: strings.TrimRight(config.BaseURL, "/"), token: config.Token, timeout: timeout,
		batchSize: batchSize, flushInterval: flushInterval, maxBodyBytes: maxBodyBytes,
		httpClient: httpClient, onDrop: config.OnDrop, fallbackWriter: config.FallbackWriter,
		queue: make(chan Event, queueSize), done: make(chan struct{}),
	}
	if client.fallbackWriter == nil {
		client.fallbackWriter = os.Stderr
	}
	go client.run()
	return client, nil
}

func validateClientConfig(config ClientConfig) error {
	parsed, err := url.Parse(config.BaseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return errors.New("logging base URL must be an absolute HTTP or HTTPS URL")
	}
	if strings.TrimSpace(config.Token) == "" {
		return errors.New("logging service token is required")
	}
	if config.Timeout < 0 || config.QueueSize < 0 || config.BatchSize < 0 || config.FlushInterval < 0 || config.MaxBodyBytes < 0 {
		return errors.New("logging client limits must not be negative")
	}
	return nil
}

// Emit normalizes and queues one event without waiting for network delivery.
func (client *Client) Emit(_ context.Context, event Event) bool {
	if event.EventID == "" {
		id, err := NewEventID()
		if err != nil {
			client.drop(event, err)
			return false
		}
		event.EventID = id
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	event.Timestamp = event.Timestamp.UTC()
	event.Metadata = SanitizeMetadata(event.Metadata)
	if err := event.Validate(); err != nil {
		client.drop(event, err)
		return false
	}

	client.mu.RLock()
	if client.closed {
		client.mu.RUnlock()
		client.drop(event, ErrClientClosed)
		return false
	}
	select {
	case client.queue <- event:
		client.mu.RUnlock()
		return true
	default:
		client.mu.RUnlock()
		client.drop(event, ErrQueueFull)
		return false
	}
}

// Close rejects new events and waits for queued batches to drain.
func (client *Client) Close(ctx context.Context) error {
	client.closeOnce.Do(func() {
		client.mu.Lock()
		client.closed = true
		close(client.queue)
		client.mu.Unlock()
	})
	select {
	case <-client.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (client *Client) run() {
	defer close(client.done)
	ticker := time.NewTicker(client.flushInterval)
	defer ticker.Stop()
	pending := make([]Event, 0, client.batchSize)
	flush := func() {
		if len(pending) == 0 {
			return
		}
		batch := append([]Event(nil), pending...)
		pending = pending[:0]
		client.sendBatch(batch)
	}
	for {
		select {
		case event, ok := <-client.queue:
			if !ok {
				flush()
				return
			}
			pending = append(pending, event)
			if len(pending) >= client.batchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

func (client *Client) sendBatch(events []Event) {
	payload, err := json.Marshal(BatchIngestRequest{Events: events})
	if err != nil {
		client.dropBatch(events, err)
		return
	}
	if len(payload) > client.maxBodyBytes {
		if len(events) == 1 {
			client.drop(events[0], ErrEventTooLarge)
			return
		}
		midpoint := len(events) / 2
		client.sendBatch(events[:midpoint])
		client.sendBatch(events[midpoint:])
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), client.timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+"/v1/log-events/batch", bytes.NewReader(payload))
	if err != nil {
		client.dropBatch(events, err)
		return
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Logging-Service-Token", client.token)
	response, err := client.httpClient.Do(request)
	if err != nil {
		client.dropBatch(events, err)
		return
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		client.dropBatch(events, fmt.Errorf("logging service returned %d", response.StatusCode))
		return
	}
	var envelope struct {
		sharedapi.Envelope
		Data IngestResult `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		client.dropBatch(events, err)
		return
	}
	if (envelope.Code != http.StatusOK && envelope.Code != http.StatusAccepted) || envelope.Code != response.StatusCode || envelope.Data.Accepted != len(events) {
		client.dropBatch(events, fmt.Errorf("invalid accepted count: code=%d accepted=%d expected=%d", envelope.Code, envelope.Data.Accepted, len(events)))
	}
}

func (client *Client) dropBatch(events []Event, err error) {
	for _, event := range events {
		client.drop(event, err)
	}
}

func (client *Client) drop(event Event, err error) {
	if client.onDrop == nil {
		client.fallbackWarning(err)
		return
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			client.fallbackWarning(fmt.Errorf("logging drop handler panicked: %v", recovered))
		}
	}()
	client.onDrop(event, err)
}

func (client *Client) fallbackWarning(err error) {
	client.fallbackMu.Lock()
	defer client.fallbackMu.Unlock()
	now := time.Now()
	if !client.lastFallbackWarning.IsZero() && now.Sub(client.lastFallbackWarning) < 30*time.Second {
		return
	}
	client.lastFallbackWarning = now
	_, _ = fmt.Fprintf(client.fallbackWriter, "[stellarmesh-logging-fallback] %v\n", err)
}
