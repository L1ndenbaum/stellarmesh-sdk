package application

import (
	"context"
	"errors"
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

func (publisher *recordingPublisher) Publish(_ context.Context, events []sharedlogging.Event) error {
	publisher.mu.Lock()
	defer publisher.mu.Unlock()
	publisher.events = append(publisher.events, events...)
	return nil
}

func TestServiceFlushesAndDrains(t *testing.T) {
	publisher := &recordingPublisher{}
	service := New(Config{FlushInterval: time.Hour, MaxBatchSize: 2}, nil, nil, publisher)
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
}

func TestServiceQueueCapacityCountsEventsAcrossRequests(t *testing.T) {
	observer := &recordingObserver{}
	service := New(Config{
		QueueCapacityEvents: 3, MaxRequestEvents: 3, Observer: observer,
	}, nil, nil, nil)
	if err := service.Ingest(context.Background(), []sharedlogging.Event{
		validApplicationEvent(t, "first"), validApplicationEvent(t, "second"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.Ingest(context.Background(), []sharedlogging.Event{
		validApplicationEvent(t, "third"), validApplicationEvent(t, "fourth"),
	}); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("error = %v", err)
	}
	observer.mu.Lock()
	defer observer.mu.Unlock()
	if observer.queueDepth != 2 || observer.results["rejected:queue_full"] != 2 {
		t.Fatalf("queue depth=%d results=%v", observer.queueDepth, observer.results)
	}
}

func TestServiceMarksNotReadyWhenKafkaAndFallbackFail(t *testing.T) {
	observer := &recordingObserver{ready: true}
	service := New(Config{Observer: observer}, nil, failingSink{}, failingPublisher{})
	service.writeBatch(context.Background(), []sharedlogging.Event{validApplicationEvent(t, "event")})
	observer.mu.Lock()
	defer observer.mu.Unlock()
	if observer.ready {
		t.Fatal("readiness remained true after Kafka and fallback failure")
	}
	if observer.results["kafka:failed"] != 1 {
		t.Fatalf("results = %v", observer.results)
	}
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
