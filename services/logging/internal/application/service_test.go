package application

import (
	"context"
	"sync"
	"testing"
	"time"

	sharedlogging "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging"
)

type recordingPublisher struct {
	mu     sync.Mutex
	events []sharedlogging.Event
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
