package application

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	sharedlogging "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging"
)

type recordingPublisher struct {
	mu     sync.Mutex
	events []sharedlogging.Event
}

type recordingObserver struct {
	mu         sync.Mutex
	ready      bool
	queueDepth int
	queueBytes int64
	results    map[string]int
}

func (observer *recordingObserver) ObserveIngest(result, reason string, count int) {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	if observer.results == nil {
		observer.results = map[string]int{}
	}
	observer.results[result+":"+reason] += count
}

func (observer *recordingObserver) SetQueueDepth(depth int) {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	observer.queueDepth = depth
}

func (observer *recordingObserver) SetQueueBytes(size int64) {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	observer.queueBytes = size
}

func (observer *recordingObserver) ObserveKafkaPublish(result string, count int) {
	observer.ObserveIngest("kafka", result, count)
}

func (observer *recordingObserver) SetReady(ready bool) {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	observer.ready = ready
}

type failingPublisher struct{}

func (failingPublisher) Publish(context.Context, []sharedlogging.Event) error {
	return errors.New("kafka unavailable")
}

type failingSink struct{}

func (failingSink) WriteBatch(context.Context, []sharedlogging.Event) error {
	return errors.New("spool unavailable")
}

type successfulSink struct {
	mu     sync.Mutex
	events []sharedlogging.Event
}

func (sink *successfulSink) WriteBatch(_ context.Context, events []sharedlogging.Event) error {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.events = append(sink.events, events...)
	return nil
}

type blockingPublisher struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (publisher *blockingPublisher) Publish(ctx context.Context, _ []sharedlogging.Event) error {
	publisher.once.Do(func() { close(publisher.started) })
	select {
	case <-publisher.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (publisher *recordingPublisher) Publish(_ context.Context, events []sharedlogging.Event) error {
	publisher.mu.Lock()
	defer publisher.mu.Unlock()
	publisher.events = append(publisher.events, events...)
	return nil
}

func TestServiceFlushesAndDrains(t *testing.T) {
	publisher := &recordingPublisher{}
	service := New(Config{FlushInterval: time.Hour, MaxBatchSize: 1}, nil, nil, publisher)
	service.Start(context.Background())
	for _, message := range []string{"first", "second"} {
		id, err := sharedlogging.NewEventID()
		if err != nil {
			t.Fatal(err)
		}
		event := sharedlogging.Event{
			EventID: id, Timestamp: time.Now(), Level: sharedlogging.LevelInfo,
			Service: "test", Message: message, Metadata: map[string]any{},
		}
		if err := service.Ingest(context.Background(), []sharedlogging.Event{event}); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := service.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	publisher.mu.Lock()
	defer publisher.mu.Unlock()
	if len(publisher.events) != 2 {
		t.Fatalf("events = %#v", publisher.events)
	}
}

func TestServiceRejectsInvalidAndOversizedRequests(t *testing.T) {
	service := New(Config{MaxRequestEvents: 1}, nil, nil, nil)
	if err := service.Ingest(context.Background(), []sharedlogging.Event{{}}); err == nil {
		t.Fatal("Ingest() accepted invalid event")
	}
	if err := service.Ingest(context.Background(), make([]sharedlogging.Event, 2)); err != ErrTooManyEvents {
		t.Fatalf("error = %v", err)
	}
	oversized := validApplicationEvent(t, strings.Repeat("x", sharedlogging.MaxEventJSONBytesV1))
	if err := service.Ingest(context.Background(), []sharedlogging.Event{oversized}); !errors.Is(err, ErrEventTooLarge) {
		t.Fatalf("oversized error = %v", err)
	}
}

func TestServiceQueueCapacityCountsEventsAcrossRequests(t *testing.T) {
	observer := &recordingObserver{}
	publisher := &recordingPublisher{}
	service := New(Config{
		QueueCapacityEvents: 3, MaxBatchSize: 2, MaxRequestEvents: 3, Observer: observer,
	}, nil, nil, publisher)
	firstResult := make(chan error, 1)
	go func() {
		firstResult <- service.Ingest(context.Background(), []sharedlogging.Event{
			validApplicationEvent(t, "first"), validApplicationEvent(t, "second"),
		})
	}()
	waitForQueueDepth(t, observer, 2)
	if err := service.Ingest(context.Background(), []sharedlogging.Event{
		validApplicationEvent(t, "third"), validApplicationEvent(t, "fourth"),
	}); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("error = %v", err)
	}
	service.Start(context.Background())
	if err := <-firstResult; err != nil {
		t.Fatal(err)
	}
	observer.mu.Lock()
	if observer.results["rejected:queue_full"] != 2 {
		t.Fatalf("queue depth=%d results=%v", observer.queueDepth, observer.results)
	}
	observer.mu.Unlock()
	if err := service.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestServiceQueueCapacityCountsSerializedBytes(t *testing.T) {
	observer := &recordingObserver{}
	publisher := &recordingPublisher{}
	first := validApplicationEvent(t, "first")
	payload, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	service := New(Config{
		QueueCapacityEvents: 2, QueueCapacityBytes: int64(len(payload) + 1),
		MaxBatchSize: 1, MaxRequestEvents: 1, Observer: observer,
	}, nil, nil, publisher)
	firstResult := make(chan error, 1)
	go func() { firstResult <- service.Ingest(context.Background(), []sharedlogging.Event{first}) }()
	waitForQueueDepth(t, observer, 1)
	if err := service.Ingest(context.Background(), []sharedlogging.Event{validApplicationEvent(t, "second")}); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("error = %v", err)
	}
	service.Start(context.Background())
	if err := <-firstResult; err != nil {
		t.Fatal(err)
	}
	observer.mu.Lock()
	if observer.queueBytes != 0 {
		t.Fatalf("queue bytes = %d", observer.queueBytes)
	}
	observer.mu.Unlock()
	if err := service.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestServiceMarksNotReadyWhenKafkaAndFallbackFail(t *testing.T) {
	observer := &recordingObserver{ready: true}
	service := New(Config{Observer: observer}, nil, failingSink{}, failingPublisher{})
	err := service.writeBatch(context.Background(), []sharedlogging.Event{validApplicationEvent(t, "event")})
	if !errors.Is(err, ErrDurabilityUnavailable) {
		t.Fatalf("error = %v", err)
	}
	observer.mu.Lock()
	defer observer.mu.Unlock()
	if observer.ready {
		t.Fatal("readiness remained true after Kafka and fallback failure")
	}
	if observer.results["kafka:failed"] != 1 {
		t.Fatalf("results = %v", observer.results)
	}
}

func TestServiceWaitsForDurablePublish(t *testing.T) {
	publisher := &blockingPublisher{started: make(chan struct{}), release: make(chan struct{})}
	service := New(Config{MaxBatchSize: 1}, nil, nil, publisher)
	service.Start(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- service.Ingest(context.Background(), []sharedlogging.Event{validApplicationEvent(t, "event")})
	}()
	<-publisher.started
	select {
	case err := <-result:
		t.Fatalf("Ingest() returned before durable publish: %v", err)
	default:
	}
	close(publisher.release)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if err := service.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestServiceAcceptsDurableFallback(t *testing.T) {
	fallback := &successfulSink{}
	service := New(Config{MaxBatchSize: 1}, nil, fallback, failingPublisher{})
	service.Start(context.Background())
	if err := service.Ingest(context.Background(), []sharedlogging.Event{validApplicationEvent(t, "event")}); err != nil {
		t.Fatal(err)
	}
	if err := service.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	fallback.mu.Lock()
	defer fallback.mu.Unlock()
	if len(fallback.events) != 1 {
		t.Fatalf("fallback events = %#v", fallback.events)
	}
}

func TestServiceShutdownCancelsAndJoinsBlockedPublish(t *testing.T) {
	publisher := &blockingPublisher{started: make(chan struct{}), release: make(chan struct{})}
	service := New(Config{MaxBatchSize: 1, PublishTimeout: time.Hour}, nil, nil, publisher)
	service.Start(context.Background())
	ingestResult := make(chan error, 1)
	go func() {
		ingestResult <- service.Ingest(context.Background(), []sharedlogging.Event{validApplicationEvent(t, "event")})
	}()
	<-publisher.started
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := service.Shutdown(shutdownCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if err := <-ingestResult; !errors.Is(err, ErrDurabilityUnavailable) {
		t.Fatalf("Ingest() error = %v", err)
	}
}

func waitForQueueDepth(t *testing.T, observer *recordingObserver, expected int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		observer.mu.Lock()
		depth := observer.queueDepth
		observer.mu.Unlock()
		if depth == expected {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("queue depth did not reach %d", expected)
}

func validApplicationEvent(t *testing.T, message string) sharedlogging.Event {
	t.Helper()
	id, err := sharedlogging.NewEventID()
	if err != nil {
		t.Fatal(err)
	}
	return sharedlogging.Event{
		EventID: id, Timestamp: time.Now(), Level: sharedlogging.LevelInfo,
		Service: "test", Message: message, Metadata: map[string]any{},
	}
}
