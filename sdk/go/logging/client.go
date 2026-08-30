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
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultClientTimeout       = 7 * time.Second
	defaultClientQueueSize     = 4096
	defaultClientBatchSize     = 128
	defaultClientFlushInterval = 100 * time.Millisecond
	defaultMaxBatchBodyBytes   = MaxHTTPBodyBytesV2
	defaultClientQueueBytes    = 16 << 20
	defaultClientMaxAttempts   = 3
	defaultInitialBackoff      = 100 * time.Millisecond
	defaultMaxBackoff          = time.Second
	defaultMaxRetryAfter       = 30 * time.Second
	maxClientQueueEvents       = 1_000_000
	maxClientQueueBytes        = int64(1 << 30)
	maxClientBatchEvents       = 10_000
	maxClientAttempts          = 10
	maxClientDuration          = time.Hour
)

var (
	ErrClientClosed  = errors.New("logging client is closed")
	ErrQueueFull     = errors.New("logging client queue is full")
	ErrEventTooLarge = errors.New("logging event exceeds the client body limit")
)

// DropHandler 接收无法入队或投递的事件。
type DropHandler func(Event, error)

// ClientConfig 控制异步 HTTP 投递。
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
	MaxRetryAfter  time.Duration
	HTTPClient     *http.Client
	OnDrop         DropHandler
	FallbackWriter io.Writer
}

// Client 异步投递 v2 日志批次。
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
	maxRetryAfter       time.Duration
	httpClient          *http.Client
	onDrop              DropHandler
	fallbackWriter      io.Writer
	queue               chan queuedEvent
	queueCapacityEvents int
	queueCapacityBytes  int64
	queuedEvents        int
	queuedBytes         int64
	done                chan struct{}
	lifecycleCtx        context.Context
	cancelLifecycle     context.CancelFunc
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

type ingestEnvelope struct {
	Code int          `json:"code"`
	Data IngestResult `json:"data"`
}

// NewClient 创建并启动异步日志客户端。
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
	maxRetryAfter := config.MaxRetryAfter
	if maxRetryAfter <= 0 {
		maxRetryAfter = defaultMaxRetryAfter
	}
	if maxRetryAfter < maxBackoff {
		return nil, errors.New("logging maximum Retry-After must not be less than maximum backoff")
	}
	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: timeout}
	}
	lifecycleCtx, cancelLifecycle := context.WithCancel(context.Background())
	client := &Client{
		baseURL: strings.TrimRight(config.BaseURL, "/"), token: config.Token, timeout: timeout,
		batchSize: batchSize, flushInterval: flushInterval, maxBodyBytes: maxBodyBytes,
		maxAttempts: maxAttempts, initialBackoff: initialBackoff, maxBackoff: maxBackoff,
		maxRetryAfter: maxRetryAfter,
		httpClient:    httpClient, onDrop: config.OnDrop, fallbackWriter: config.FallbackWriter,
		queue: make(chan queuedEvent, queueSize), queueCapacityEvents: queueSize,
		queueCapacityBytes: queueBytes, done: make(chan struct{}),
		lifecycleCtx: lifecycleCtx, cancelLifecycle: cancelLifecycle,
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
		config.InitialBackoff < 0 || config.MaxBackoff < 0 || config.MaxRetryAfter < 0 {
		return errors.New("logging client limits must not be negative")
	}
	if config.MaxBodyBytes > MaxHTTPBodyBytesV2 {
		return fmt.Errorf("logging max body bytes must not exceed %d", MaxHTTPBodyBytesV2)
	}
	if config.QueueSize > maxClientQueueEvents || config.QueueBytes > maxClientQueueBytes ||
		config.BatchSize > maxClientBatchEvents || config.MaxAttempts > maxClientAttempts {
		return errors.New("logging client capacity is outside supported bounds")
	}
	for _, duration := range []time.Duration{
		config.Timeout, config.FlushInterval, config.InitialBackoff, config.MaxBackoff, config.MaxRetryAfter,
	} {
		if duration > maxClientDuration {
			return errors.New("logging client duration is outside supported bounds")
		}
	}
	if config.InitialBackoff > 0 && config.MaxBackoff > 0 && config.InitialBackoff > config.MaxBackoff {
		return errors.New("logging initial backoff must not exceed maximum backoff")
	}
	return nil
}

// Emit 保持 Emitter 兼容，并把事件交给独立生命周期管理的异步队列。
func (client *Client) Emit(_ context.Context, event Event) bool {
	return client.Enqueue(event)
}

// Enqueue 规范化并入队一个事件，不等待网络投递。
func (client *Client) Enqueue(event Event) bool {
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
	if eventBytes > MaxEventJSONBytesV2 {
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

// Close 拒绝新事件并等待队列中的批次排空。
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
		client.cancelLifecycle()
		return ctx.Err()
	}
}

func (client *Client) run() {
	defer close(client.done)
	defer client.cancelLifecycle()
	ticker := time.NewTicker(client.flushInterval)
	defer ticker.Stop()
	pending := make([]queuedEvent, 0, client.batchSize)
	flush := func() {
		if len(pending) == 0 {
			return
		}
		batch := append([]queuedEvent(nil), pending...)
		pending = pending[:0]
		client.sendBatch(client.lifecycleCtx, batch)
		client.release(batch)
	}
	for {
		select {
		case <-client.lifecycleCtx.Done():
			flush()
			client.dropQueued(client.lifecycleCtx.Err())
			return
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

func (client *Client) sendBatch(ctx context.Context, queued []queuedEvent) {
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
		client.sendBatch(ctx, queued[:midpoint])
		client.sendBatch(ctx, queued[midpoint:])
		return
	}
	if err := ctx.Err(); err != nil {
		client.dropBatch(events, errors.Join(ErrClientClosed, err))
		return
	}
	var lastErr error
	for attempt := 1; attempt <= client.maxAttempts; attempt++ {
		retryable, retryAfter, err := client.sendOnce(ctx, events, payload)
		if err == nil {
			return
		}
		lastErr = err
		if ctx.Err() != nil {
			lastErr = errors.Join(ErrClientClosed, ctx.Err())
			break
		}
		if !retryable || attempt == client.maxAttempts {
			break
		}
		if !waitForRetry(ctx, client.retryDelay(attempt, retryAfter)) {
			lastErr = errors.Join(ErrClientClosed, ctx.Err())
			break
		}
	}
	client.dropBatch(events, lastErr)
}

func (client *Client) sendOnce(ctx context.Context, events []Event, payload []byte) (bool, time.Duration, error) {
	ctx, cancel := context.WithTimeout(ctx, client.timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+"/v2/log-events/batch", bytes.NewReader(payload))
	if err != nil {
		return false, 0, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Logging-Service-Token", client.token)
	response, err := client.httpClient.Do(request)
	if err != nil {
		return true, 0, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, response.Body)
		return retryableStatus(response.StatusCode), parseRetryAfter(response.Header.Get("Retry-After"), time.Now(), client.maxRetryAfter), fmt.Errorf("logging service returned %d", response.StatusCode)
	}
	var envelope ingestEnvelope
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		return false, 0, err
	}
	if (envelope.Code != http.StatusOK && envelope.Code != http.StatusAccepted) || envelope.Code != response.StatusCode || envelope.Data.Accepted != len(events) {
		return false, 0, fmt.Errorf("invalid accepted count: code=%d accepted=%d expected=%d", envelope.Code, envelope.Data.Accepted, len(events))
	}
	return false, 0, nil
}

func waitForRetry(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func (client *Client) dropQueued(cause error) {
	for queued := range client.queue {
		client.drop(queued.event, errors.Join(ErrClientClosed, cause))
		client.release([]queuedEvent{queued})
	}
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

func (client *Client) retryDelay(failedAttempt int, retryAfter time.Duration) time.Duration {
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
	if delay > 1 {
		delay = time.Duration(rand.Int64N(int64(delay))) + 1
	}
	if retryAfter > delay {
		return retryAfter
	}
	return delay
}

func parseRetryAfter(value string, now time.Time, maximum time.Duration) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" || maximum <= 0 {
		return 0
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds < 0 {
			return 0
		}
		if seconds > int64(maximum/time.Second) {
			return maximum
		}
		return min(time.Duration(seconds)*time.Second, maximum)
	}
	when, err := http.ParseTime(value)
	if err != nil {
		return 0
	}
	delay := when.Sub(now)
	if delay <= 0 {
		return 0
	}
	return min(delay, maximum)
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
