package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
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
	defaultClientQueueBytes    = 16 << 20
	defaultClientMaxAttempts   = 3
	defaultInitialBackoff      = 100 * time.Millisecond
	defaultMaxBackoff          = time.Second
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
	QueueBytes     int64
	BatchSize      int
	FlushInterval  time.Duration
	MaxBodyBytes   int
	MaxAttempts    int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
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
	maxAttempts         int
	initialBackoff      time.Duration
	maxBackoff          time.Duration
	httpClient          *http.Client
	onDrop              DropHandler
	fallbackWriter      io.Writer
	queue               chan queuedEvent
	queueCapacityEvents int
	queueCapacityBytes  int64
	queuedEvents        int
	queuedBytes         int64
	done                chan struct{}
	mu                  sync.RWMutex
	closed              bool
	closeOnce           sync.Once
	fallbackMu          sync.Mutex
	lastFallbackWarning time.Time
}

type queuedEvent struct {
	event Event
	bytes int64
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
	queueBytes := config.QueueBytes
	if queueBytes <= 0 {
		queueBytes = defaultClientQueueBytes
	}
	maxAttempts := config.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = defaultClientMaxAttempts
	}
	initialBackoff := config.InitialBackoff
	if initialBackoff <= 0 {
		initialBackoff = defaultInitialBackoff
	}
	maxBackoff := config.MaxBackoff
	if maxBackoff <= 0 {
		maxBackoff = defaultMaxBackoff
	}
	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: timeout}
	}
	client := &Client{
		baseURL: strings.TrimRight(config.BaseURL, "/"), token: config.Token, timeout: timeout,
		batchSize: batchSize, flushInterval: flushInterval, maxBodyBytes: maxBodyBytes,
		maxAttempts: maxAttempts, initialBackoff: initialBackoff, maxBackoff: maxBackoff,
		httpClient: httpClient, onDrop: config.OnDrop, fallbackWriter: config.FallbackWriter,
		queue: make(chan queuedEvent, queueSize), queueCapacityEvents: queueSize,
		queueCapacityBytes: queueBytes, done: make(chan struct{}),
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
	if config.Timeout < 0 || config.QueueSize < 0 || config.QueueBytes < 0 || config.BatchSize < 0 ||
		config.FlushInterval < 0 || config.MaxBodyBytes < 0 || config.MaxAttempts < 0 ||
		config.InitialBackoff < 0 || config.MaxBackoff < 0 {
		return errors.New("logging client limits must not be negative")
	}
	if config.MaxBodyBytes > MaxHTTPBodyBytesV1 {
		return fmt.Errorf("logging max body bytes must not exceed %d", MaxHTTPBodyBytesV1)
	}
	if config.InitialBackoff > 0 && config.MaxBackoff > 0 && config.InitialBackoff > config.MaxBackoff {
		return errors.New("logging initial backoff must not exceed maximum backoff")
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
	payload, err := json.Marshal(event)
	if err != nil {
		client.drop(event, err)
		return false
	}
	eventBytes := int64(len(payload))
	if eventBytes > MaxEventJSONBytesV1 {
		client.drop(event, ErrEventTooLarge)
		return false
	}

	client.mu.Lock()
	if client.closed {
		client.mu.Unlock()
		client.drop(event, ErrClientClosed)
		return false
	}
	if client.queuedEvents >= client.queueCapacityEvents ||
		eventBytes > client.queueCapacityBytes-client.queuedBytes {
		client.mu.Unlock()
		client.drop(event, ErrQueueFull)
		return false
	}
	select {
	case client.queue <- queuedEvent{event: event, bytes: eventBytes}:
		client.queuedEvents++
		client.queuedBytes += eventBytes
		client.mu.Unlock()
		return true
	default:
		client.mu.Unlock()
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
	pending := make([]queuedEvent, 0, client.batchSize)
	flush := func() {
		if len(pending) == 0 {
			return
		}
		batch := append([]queuedEvent(nil), pending...)
		pending = pending[:0]
		client.sendBatch(batch)
		client.release(batch)
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

func (client *Client) sendBatch(queued []queuedEvent) {
	events := make([]Event, 0, len(queued))
	for _, item := range queued {
		events = append(events, item.event)
	}
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
		client.sendBatch(queued[:midpoint])
		client.sendBatch(queued[midpoint:])
		return
	}
	var lastErr error
	for attempt := 1; attempt <= client.maxAttempts; attempt++ {
		retryable, err := client.sendOnce(events, payload)
		if err == nil {
			return
		}
		lastErr = err
		if !retryable || attempt == client.maxAttempts {
			break
		}
		time.Sleep(client.retryDelay(attempt))
	}
	client.dropBatch(events, lastErr)
}

func (client *Client) sendOnce(events []Event, payload []byte) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), client.timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+"/v1/log-events/batch", bytes.NewReader(payload))
	if err != nil {
		return false, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Logging-Service-Token", client.token)
	response, err := client.httpClient.Do(request)
	if err != nil {
		return true, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, response.Body)
		return retryableStatus(response.StatusCode), fmt.Errorf("logging service returned %d", response.StatusCode)
	}
	var envelope struct {
		sharedapi.Envelope
		Data IngestResult `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		return false, err
	}
	if (envelope.Code != http.StatusOK && envelope.Code != http.StatusAccepted) || envelope.Code != response.StatusCode || envelope.Data.Accepted != len(events) {
		return false, fmt.Errorf("invalid accepted count: code=%d accepted=%d expected=%d", envelope.Code, envelope.Data.Accepted, len(events))
	}
	return false, nil
}

func retryableStatus(status int) bool {
	switch status {
	case http.StatusRequestTimeout, http.StatusTooEarly, http.StatusTooManyRequests,
		http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func (client *Client) retryDelay(failedAttempt int) time.Duration {
	delay := client.initialBackoff
	for current := 1; current < failedAttempt && delay < client.maxBackoff; current++ {
		if delay > client.maxBackoff/2 {
			delay = client.maxBackoff
			break
		}
		delay *= 2
	}
	if delay > client.maxBackoff {
		delay = client.maxBackoff
	}
	if delay <= 1 {
		return delay
	}
	return time.Duration(rand.Int64N(int64(delay))) + 1
}

func (client *Client) release(events []queuedEvent) {
	var releasedBytes int64
	for _, event := range events {
		releasedBytes += event.bytes
	}
	client.mu.Lock()
	client.queuedEvents -= len(events)
	client.queuedBytes -= releasedBytes
	client.mu.Unlock()
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
