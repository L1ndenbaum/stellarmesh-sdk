package filesink

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	sharedlogging "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging"
)

type recordingPublisher struct {
	events []sharedlogging.Event
}

func (publisher *recordingPublisher) Publish(_ context.Context, events []sharedlogging.Event) error {
	publisher.events = append(publisher.events, events...)
	return nil
}

func TestFallbackStorePartitionsAndReplays(t *testing.T) {
	directory := t.TempDir()
	store := NewKafkaFallbackStore(filepath.Join(directory, "events.jsonl"), filepath.Join(directory, "error.jsonl"))
	events := []sharedlogging.Event{
		validEvent(t, sharedlogging.LevelInfo),
		validEvent(t, sharedlogging.LevelError),
	}
	if err := store.WriteBatch(context.Background(), events); err != nil {
		t.Fatal(err)
	}
	publisher := &recordingPublisher{}
	if err := store.ReplayOnce(context.Background(), publisher); err != nil {
		t.Fatal(err)
	}
	if len(publisher.events) != 2 {
		t.Fatalf("events = %#v", publisher.events)
	}
}

func validEvent(t *testing.T, level sharedlogging.Level) sharedlogging.Event {
	t.Helper()
	id, err := sharedlogging.NewEventID()
	if err != nil {
		t.Fatal(err)
	}
	return sharedlogging.Event{
		EventID: id, Timestamp: time.Now(), Level: level, Service: "test", Message: "event", Metadata: map[string]any{},
	}
}
