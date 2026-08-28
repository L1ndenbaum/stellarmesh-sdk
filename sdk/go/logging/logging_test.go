package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestKafkaPartitionKeyIsBoundedAndStable(t *testing.T) {
	event := Event{EventID: "018f16b6-3f9f-7d98-a328-3eac70bd0542", TraceID: strings.Repeat("trace", 1000)}
	first := KafkaPartitionKeyV1(event)
	second := KafkaPartitionKeyV1(event)
	if len(first) != 32 || !bytes.Equal(first, second) {
		t.Fatalf("trace key length=%d stable=%v", len(first), bytes.Equal(first, second))
	}
	event.TraceID = ""
	if got := string(KafkaPartitionKeyV1(event)); got != event.EventID {
		t.Fatalf("fallback key = %q", got)
	}
	if !FitsKafkaKeyValueBudgetV1(event, MaxEventJSONBytesV1) {
		t.Fatal("maximum canonical event does not fit the Kafka key/value budget")
	}
}

func TestSanitizeMetadata(t *testing.T) {
	type credentials struct {
		Password string `json:"password"`
		Safe     string `json:"safe"`
	}
	got := SanitizeMetadata(map[string]any{
		"api_token": "secret",
		"apiKey":    "secret",
		"nested":    map[string]string{"password": "hidden", "safe": "value"},
		"typed":     credentials{Password: "hidden", Safe: "value"},
		"error":     errors.New("request failed"),
		"large": struct {
			Value int64 `json:"value"`
		}{Value: 9_007_199_254_740_993},
		"nan":     math.NaN(),
		"channel": make(chan int),
	})
	if got["api_token"] != redactedValue || got["apiKey"] != redactedValue {
		t.Fatalf("sensitive values = %#v", got)
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
	if got["error"] != "request failed" || got["nan"] != unserializableValue {
		t.Fatalf("special values = %#v", got)
	}
	large := got["large"].(map[string]any)["value"]
	if number, ok := large.(json.Number); !ok || number.String() != "9007199254740993" {
		t.Fatalf("large integer = %#v", large)
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

type recordingEmitter struct {
	events []Event
}

func (emitter *recordingEmitter) Emit(_ context.Context, event Event) bool {
	emitter.events = append(emitter.events, event)
	return true
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

func TestClientRetriesTransientStatusWithStableEventID(t *testing.T) {
	attempts := 0
	var eventIDs []string
	client, err := NewClient(ClientConfig{
		BaseURL: "http://logging-service", Token: "token", BatchSize: 1,
		MaxAttempts: 3, InitialBackoff: time.Nanosecond, MaxBackoff: time.Nanosecond,
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			attempts++
			var batch BatchIngestRequest
			if decodeErr := json.NewDecoder(request.Body).Decode(&batch); decodeErr != nil {
				return nil, decodeErr
			}
			eventIDs = append(eventIDs, batch.Events[0].EventID)
			if attempts < 3 {
				return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: io.NopCloser(strings.NewReader("unavailable"))}, nil
			}
			payload := `{"code":202,"message":"ok","data":{"accepted":1},"timestamp":"2026-08-01T12:00:00Z"}`
			return &http.Response{StatusCode: http.StatusAccepted, Body: io.NopCloser(strings.NewReader(payload))}, nil
		})},
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
	if attempts != 3 || len(eventIDs) != 3 || eventIDs[0] != eventIDs[1] || eventIDs[1] != eventIDs[2] {
		t.Fatalf("attempts=%d event_ids=%v", attempts, eventIDs)
	}
}

func TestClientDoesNotRetryPermanentStatus(t *testing.T) {
	attempts := 0
	dropped := make(chan error, 1)
	client, err := NewClient(ClientConfig{
		BaseURL: "http://logging-service", Token: "token", BatchSize: 1, MaxAttempts: 3,
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			attempts++
			return &http.Response{StatusCode: http.StatusBadRequest, Body: io.NopCloser(strings.NewReader("bad request"))}, nil
		})},
		OnDrop: func(_ Event, err error) { dropped <- err },
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
	if attempts != 1 {
		t.Fatalf("attempts = %d", attempts)
	}
	select {
	case <-dropped:
	default:
		t.Fatal("drop handler was not called")
	}
}

func TestClientQueueBytesIncludeInFlightBatch(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	event := Event{
		EventID:   "018f16b6-3f9f-7d98-a328-3eac70bd0542",
		Timestamp: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC),
		Level:     LevelInfo, Service: "test", Message: "event", Metadata: map[string]any{},
	}
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	dropped := make(chan error, 1)
	client, err := NewClient(ClientConfig{
		BaseURL: "http://logging-service", Token: "token", QueueSize: 2, QueueBytes: int64(len(payload)), BatchSize: 1,
		MaxAttempts: 1,
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			close(started)
			<-release
			body := `{"code":202,"message":"ok","data":{"accepted":1},"timestamp":"2026-08-01T12:00:00Z"}`
			return &http.Response{StatusCode: http.StatusAccepted, Body: io.NopCloser(strings.NewReader(body))}, nil
		})},
		OnDrop: func(_ Event, err error) { dropped <- err },
	})
	if err != nil {
		t.Fatal(err)
	}
	if !client.Emit(context.Background(), event) {
		t.Fatal("first Emit() = false")
	}
	<-started
	if client.Emit(context.Background(), event) {
		t.Fatal("second Emit() = true")
	}
	close(release)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.Close(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-dropped:
		if !errors.Is(err, ErrQueueFull) {
			t.Fatalf("drop error = %v", err)
		}
	default:
		t.Fatal("drop handler was not called")
	}
}

func TestClientDefaultBodyLimitIncludesBatchEnvelope(t *testing.T) {
	client, err := NewClient(ClientConfig{BaseURL: "http://logging-service", Token: "token"})
	if err != nil {
		t.Fatal(err)
	}
	if client.maxBodyBytes != MaxHTTPBodyBytesV1 {
		t.Fatalf("max body bytes = %d", client.maxBodyBytes)
	}
	if client.timeout != 7*time.Second || client.maxRetryAfter != 30*time.Second {
		t.Fatalf("timeout=%s max_retry_after=%s", client.timeout, client.maxRetryAfter)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestClientDefaultBodyLimitSendsMaximumCanonicalEvent(t *testing.T) {
	event := Event{
		EventID:   "018f16b6-3f9f-7d98-a328-3eac70bd0542",
		Timestamp: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC),
		Level:     LevelInfo, Service: "test", Message: "x", Metadata: map[string]any{},
	}
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	event.Message = strings.Repeat("x", MaxEventJSONBytesV1-(len(payload)-1))
	payload, err = json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if len(payload) != MaxEventJSONBytesV1 {
		t.Fatalf("event bytes = %d", len(payload))
	}
	requests := 0
	client, err := NewClient(ClientConfig{
		BaseURL: "http://logging-service", Token: "token", BatchSize: 1,
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			requests++
			body, readErr := io.ReadAll(request.Body)
			if readErr != nil {
				return nil, readErr
			}
			if len(body) > MaxHTTPBodyBytesV1 {
				return nil, errors.New("batch body exceeded the HTTP contract limit")
			}
			response := `{"code":202,"message":"ok","data":{"accepted":1},"timestamp":"2026-08-01T12:00:00Z"}`
			return &http.Response{StatusCode: http.StatusAccepted, Body: io.NopCloser(strings.NewReader(response))}, nil
		})},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !client.Emit(context.Background(), event) {
		t.Fatal("Emit() = false")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d", requests)
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
	if _, err := NewClient(ClientConfig{
		BaseURL: "http://logging-service", Token: "token", QueueBytes: 2 << 30,
	}); err == nil {
		t.Fatal("NewClient() accepted an unsafe queue byte limit")
	}
	if _, err := NewClient(ClientConfig{
		BaseURL: "http://logging-service", Token: "token", MaxBackoff: time.Second, MaxRetryAfter: time.Millisecond,
	}); err == nil {
		t.Fatal("NewClient() accepted Retry-After below maximum backoff")
	}
	if _, err := NewLogger(LoggerConfig{}); err == nil {
		t.Fatal("NewLogger() accepted invalid configuration")
	}
	if _, err := NewLogger(LoggerConfig{Service: " backend ", Emitter: &recordingEmitter{}}); err == nil {
		t.Fatal("NewLogger() accepted an untrimmed service")
	}
}

func TestClientCloseDeadlineCancelsTransportAndDropsUnsentEvents(t *testing.T) {
	started := make(chan struct{})
	dropped := make(chan error, 2)
	attempts := 0
	client, err := NewClient(ClientConfig{
		BaseURL: "http://logging-service", Token: "token", QueueSize: 2, BatchSize: 1,
		MaxAttempts: 3, InitialBackoff: time.Second, MaxBackoff: time.Second,
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			attempts++
			if attempts == 1 {
				close(started)
			}
			<-request.Context().Done()
			return nil, request.Context().Err()
		})},
		OnDrop: func(_ Event, err error) { dropped <- err },
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range []string{"in-flight", "queued"} {
		if !client.Enqueue(Event{Level: LevelInfo, Service: "test", Message: message}) {
			t.Fatalf("Enqueue(%q) = false", message)
		}
		if message == "in-flight" {
			<-started
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := client.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close() error = %v", err)
	}
	waitCtx, waitCancel := context.WithTimeout(context.Background(), time.Second)
	defer waitCancel()
	if err := client.Close(waitCtx); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	for range 2 {
		select {
		case dropErr := <-dropped:
			if !errors.Is(dropErr, ErrClientClosed) {
				t.Fatalf("drop error = %v", dropErr)
			}
		default:
			t.Fatal("missing deterministic drop callback")
		}
	}
	if attempts != 1 {
		t.Fatalf("transport attempts = %d", attempts)
	}
}

func TestEventRejectsUntrimmedServiceButPreservesMessageWhitespace(t *testing.T) {
	event := Event{
		EventID: "018f16b6-3f9f-7d98-a328-3eac70bd0542", Timestamp: time.Now(), Level: LevelInfo,
		Service: " backend ", Message: " message\n", Metadata: map[string]any{},
	}
	if err := event.Validate(); err == nil {
		t.Fatal("Validate() accepted an untrimmed service")
	}
	event.Service = "backend"
	if err := event.Validate(); err != nil {
		t.Fatalf("Validate() rejected message whitespace: %v", err)
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	if got := parseRetryAfter("12", now, 30*time.Second); got != 12*time.Second {
		t.Fatalf("seconds delay = %s", got)
	}
	if got := parseRetryAfter("120", now, 30*time.Second); got != 30*time.Second {
		t.Fatalf("capped delay = %s", got)
	}
	when := now.Add(5 * time.Second).Format(http.TimeFormat)
	if got := parseRetryAfter(when, now, 30*time.Second); got != 5*time.Second {
		t.Fatalf("date delay = %s", got)
	}
	for _, value := range []string{"invalid", "-1", now.Add(-time.Second).Format(http.TimeFormat)} {
		if got := parseRetryAfter(value, now, 30*time.Second); got != 0 {
			t.Fatalf("parseRetryAfter(%q) = %s", value, got)
		}
	}
}
