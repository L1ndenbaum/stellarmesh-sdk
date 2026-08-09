package filesink

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sharedlogging "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging"
)

type recordingPublisher struct {
	events    []sharedlogging.Event
	failAfter int
	calls     int
}

func (publisher *recordingPublisher) Publish(_ context.Context, events []sharedlogging.Event) error {
	publisher.calls++
	if publisher.failAfter > 0 && publisher.calls >= publisher.failAfter {
		return errors.New("Kafka unavailable")
	}
	publisher.events = append(publisher.events, events...)
	return nil
}

func TestFallbackStorePrioritizesAndReplaysLargeEvents(t *testing.T) {
	store := newStore(t, Config{RootDir: filepath.Join(t.TempDir(), "spool"), SegmentBytes: 32 << 10})
	events := []sharedlogging.Event{
		validEvent(t, sharedlogging.LevelInfo, strings.Repeat("x", 70<<10)),
		validEvent(t, sharedlogging.LevelError, "priority"),
	}
	if err := store.WriteBatch(context.Background(), events); err != nil {
		t.Fatal(err)
	}
	publisher := &recordingPublisher{}
	if err := store.ReplayOnce(context.Background(), publisher); err != nil {
		t.Fatal(err)
	}
	if len(publisher.events) != 2 || publisher.events[0].Level != sharedlogging.LevelError {
		t.Fatalf("events = %#v", publisher.events)
	}
	regular, priority := store.Bytes()
	if regular != 0 || priority != 0 {
		t.Fatalf("spool bytes = %d, %d", regular, priority)
	}
}

func TestFallbackStoreEnforcesDiskBudget(t *testing.T) {
	store := newStore(t, Config{RootDir: filepath.Join(t.TempDir(), "spool"), MaxBytes: 128})
	err := store.WriteBatch(context.Background(), []sharedlogging.Event{
		validEvent(t, sharedlogging.LevelInfo, strings.Repeat("x", 256)),
	})
	if !errors.Is(err, ErrSpoolFull) {
		t.Fatalf("error = %v", err)
	}
}

func TestFallbackStoreRetainsSegmentAfterPartialReplayFailure(t *testing.T) {
	store := newStore(t, Config{
		RootDir: filepath.Join(t.TempDir(), "spool"), SegmentBytes: 1 << 20, ReplayBatchSize: 1,
	})
	if err := store.WriteBatch(context.Background(), []sharedlogging.Event{
		validEvent(t, sharedlogging.LevelInfo, "first"),
		validEvent(t, sharedlogging.LevelInfo, "second"),
	}); err != nil {
		t.Fatal(err)
	}
	publisher := &recordingPublisher{failAfter: 2}
	if err := store.ReplayOnce(context.Background(), publisher); err == nil {
		t.Fatal("ReplayOnce() succeeded unexpectedly")
	}
	regular, _ := store.Bytes()
	if regular == 0 || len(publisher.events) != 1 {
		t.Fatalf("regular=%d events=%d", regular, len(publisher.events))
	}

	retry := &recordingPublisher{}
	if err := store.ReplayOnce(context.Background(), retry); err != nil {
		t.Fatal(err)
	}
	if len(retry.events) != 2 {
		t.Fatalf("retry events = %d", len(retry.events))
	}
}

func TestFallbackStoreStopsBeforeRegularSegmentsWhenPriorityReplayFails(t *testing.T) {
	store := newStore(t, Config{RootDir: filepath.Join(t.TempDir(), "spool"), ReplayBatchSize: 1})
	if err := store.WriteBatch(context.Background(), []sharedlogging.Event{
		validEvent(t, sharedlogging.LevelInfo, "regular"),
		validEvent(t, sharedlogging.LevelError, "priority"),
	}); err != nil {
		t.Fatal(err)
	}
	publisher := &recordingPublisher{failAfter: 1}
	if err := store.ReplayOnce(context.Background(), publisher); err == nil {
		t.Fatal("ReplayOnce() succeeded unexpectedly")
	}
	if publisher.calls != 1 || len(publisher.events) != 0 {
		t.Fatalf("calls=%d events=%d", publisher.calls, len(publisher.events))
	}
}

func TestFallbackStoreRemovesInterruptedTemporaryFiles(t *testing.T) {
	root := filepath.Join(t.TempDir(), "spool")
	regular := filepath.Join(root, regularPriority)
	if err := os.MkdirAll(regular, 0o755); err != nil {
		t.Fatal(err)
	}
	temporary := filepath.Join(regular, "interrupted.tmp")
	if err := os.WriteFile(temporary, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	newStore(t, Config{RootDir: root})
	if _, err := os.Stat(temporary); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary file still exists: %v", err)
	}
}

func newStore(t *testing.T, config Config) *KafkaFallbackStore {
	t.Helper()
	store, err := NewKafkaFallbackStore(config)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func validEvent(t *testing.T, level sharedlogging.Level, payload string) sharedlogging.Event {
	t.Helper()
	id, err := sharedlogging.NewEventID()
	if err != nil {
		t.Fatal(err)
	}
	return sharedlogging.Event{
		EventID: id, Timestamp: time.Now(), Level: level, Service: "test", Message: "event",
		Metadata: map[string]any{"payload": payload},
	}
}
