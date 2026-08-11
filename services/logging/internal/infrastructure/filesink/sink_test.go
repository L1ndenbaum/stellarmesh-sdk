package filesink

import (
	"bytes"
	"context"
	"encoding/json"
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
	checkErr  error
}

type failFirstPublisher struct {
	events []sharedlogging.Event
	calls  int
	err    error
}

func (publisher *failFirstPublisher) Publish(_ context.Context, events []sharedlogging.Event) error {
	publisher.calls++
	if publisher.calls == 1 {
		return publisher.err
	}
	publisher.events = append(publisher.events, events...)
	return nil
}

type contextPublisher struct{}

type selectivePermanentPublisher struct {
	err    error
	events []sharedlogging.Event
	calls  int
}

func (publisher *selectivePermanentPublisher) Publish(_ context.Context, events []sharedlogging.Event) error {
	publisher.calls++
	for _, event := range events {
		if event.Metadata["payload"] == "reject" {
			return publisher.err
		}
	}
	publisher.events = append(publisher.events, events...)
	return nil
}

func (contextPublisher) Publish(ctx context.Context, _ []sharedlogging.Event) error {
	<-ctx.Done()
	return ctx.Err()
}

func (publisher *recordingPublisher) Check(context.Context) error { return publisher.checkErr }

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

func TestFallbackStoreReservesAtomicQuarantineCapacity(t *testing.T) {
	event := validEvent(t, sharedlogging.LevelInfo, "reject")
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	required := int64(2*(len(payload)+1)) + quarantineMetadataReserveBytes
	rejectedStore := newStore(t, Config{RootDir: filepath.Join(t.TempDir(), "spool"), MaxBytes: required - 1})
	if err := rejectedStore.WriteBatch(context.Background(), []sharedlogging.Event{event}); !errors.Is(err, ErrSpoolFull) {
		t.Fatalf("WriteBatch() error = %v", err)
	}

	root := filepath.Join(t.TempDir(), "spool")
	store := newStore(t, Config{RootDir: root, MaxBytes: required})
	if err := store.WriteBatch(context.Background(), []sharedlogging.Event{event}); err != nil {
		t.Fatal(err)
	}
	if !store.Saturated() {
		t.Fatal("store did not account for quarantine replacement reserve")
	}
	recovered := newStore(t, Config{RootDir: root, MaxBytes: required})
	if !recovered.Saturated() {
		t.Fatal("recovered store lost quarantine replacement reserve")
	}
	permanentErr := errors.New("message too large")
	recovered.isPermanentPublishError = func(err error) bool { return errors.Is(err, permanentErr) }
	if err := recovered.ReplayOnce(context.Background(), &selectivePermanentPublisher{err: permanentErr}); err != nil {
		t.Fatal(err)
	}
	recovered.mu.Lock()
	actualBytes := recovered.totalBytesLocked()
	recovered.mu.Unlock()
	if actualBytes > required || recovered.QuarantineBytes() == 0 {
		t.Fatalf("actual=%d limit=%d quarantine=%d", actualBytes, required, recovered.QuarantineBytes())
	}
}

func TestFallbackStoreCommitsMixedPrioritiesAsOneBatch(t *testing.T) {
	root := filepath.Join(t.TempDir(), "spool")
	store := newStore(t, Config{RootDir: root, SegmentBytes: 64})
	if err := store.WriteBatch(context.Background(), []sharedlogging.Event{
		validEvent(t, sharedlogging.LevelInfo, strings.Repeat("r", 128)),
		validEvent(t, sharedlogging.LevelError, strings.Repeat("p", 128)),
	}); err != nil {
		t.Fatal(err)
	}
	batches, err := os.ReadDir(filepath.Join(root, batchesDirectory))
	if err != nil {
		t.Fatal(err)
	}
	if len(batches) != 1 || !batches[0].IsDir() {
		t.Fatalf("committed batches = %#v", batches)
	}
	for _, priority := range []string{regularPriority, highPriority} {
		paths, err := segmentPaths(filepath.Join(root, batchesDirectory, batches[0].Name(), priority))
		if err != nil {
			t.Fatal(err)
		}
		if len(paths) == 0 {
			t.Fatalf("%s segments = %v", priority, paths)
		}
		legacy, err := segmentPaths(filepath.Join(root, priority))
		if err != nil {
			t.Fatal(err)
		}
		if len(legacy) != 0 {
			t.Fatalf("legacy %s segments = %v", priority, legacy)
		}
	}
}

func TestFallbackStoreDoesNotExposeFailedBatchCommit(t *testing.T) {
	root := filepath.Join(t.TempDir(), "spool")
	store := newStore(t, Config{RootDir: root})
	store.rename = func(string, string) error { return errors.New("rename failed") }
	err := store.WriteBatch(context.Background(), []sharedlogging.Event{
		validEvent(t, sharedlogging.LevelInfo, "regular"),
		validEvent(t, sharedlogging.LevelError, "priority"),
	})
	if err == nil || !strings.Contains(err.Error(), "rename failed") {
		t.Fatalf("error = %v", err)
	}
	regular, priority := store.Bytes()
	if regular != 0 || priority != 0 {
		t.Fatalf("spool bytes = %d, %d", regular, priority)
	}
	for _, directory := range []string{stagingDirectory, batchesDirectory} {
		entries, err := os.ReadDir(filepath.Join(root, directory))
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Fatalf("%s entries = %#v", directory, entries)
		}
	}
}

func TestFallbackStoreAccountsCommittedBatchWhenParentSyncFails(t *testing.T) {
	root := filepath.Join(t.TempDir(), "spool")
	store := newStore(t, Config{RootDir: root})
	store.syncDir = func(path string) error {
		if path == filepath.Join(root, batchesDirectory) {
			return errors.New("parent sync failed")
		}
		return syncDirectory(path)
	}
	err := store.WriteBatch(context.Background(), []sharedlogging.Event{
		validEvent(t, sharedlogging.LevelInfo, "regular"),
	})
	if err == nil || !strings.Contains(err.Error(), "parent sync failed") {
		t.Fatalf("error = %v", err)
	}
	regular, priority := store.Bytes()
	if regular == 0 || priority != 0 {
		t.Fatalf("spool bytes = %d, %d", regular, priority)
	}

	recovered := newStore(t, Config{RootDir: root})
	publisher := &recordingPublisher{}
	if err := recovered.ReplayOnce(context.Background(), publisher); err != nil {
		t.Fatal(err)
	}
	if len(publisher.events) != 1 {
		t.Fatalf("events = %#v", publisher.events)
	}
}

func TestFallbackStoreReplaysLegacySegments(t *testing.T) {
	root := filepath.Join(t.TempDir(), "spool")
	legacy := filepath.Join(root, regularPriority)
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	event := validEvent(t, sharedlogging.LevelInfo, "legacy")
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "legacy"+segmentSuffix), append(payload, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	store := newStore(t, Config{RootDir: root})
	publisher := &recordingPublisher{}
	if err := store.ReplayOnce(context.Background(), publisher); err != nil {
		t.Fatal(err)
	}
	if len(publisher.events) != 1 || publisher.events[0].EventID != event.EventID {
		t.Fatalf("events = %#v", publisher.events)
	}
}

func TestFallbackReplayChecksKafkaAndCanRecoverWhenEmpty(t *testing.T) {
	store := newStore(t, Config{RootDir: filepath.Join(t.TempDir(), "spool")})
	publisher := &recordingPublisher{}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	done := store.StartReplay(ctx, publisher, time.Millisecond, time.Second, func(err error) {
		select {
		case result <- err:
		default:
		}
	})
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("replay worker did not stop")
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

func TestFallbackStoreAttemptsRegularSegmentsWhenPriorityReplayFails(t *testing.T) {
	store := newStore(t, Config{RootDir: filepath.Join(t.TempDir(), "spool"), ReplayBatchSize: 1})
	if err := store.WriteBatch(context.Background(), []sharedlogging.Event{
		validEvent(t, sharedlogging.LevelInfo, "regular"),
		validEvent(t, sharedlogging.LevelError, "priority"),
	}); err != nil {
		t.Fatal(err)
	}
	publisher := &failFirstPublisher{err: errors.New("Kafka unavailable")}
	if err := store.ReplayOnce(context.Background(), publisher); err == nil {
		t.Fatal("ReplayOnce() succeeded unexpectedly")
	}
	if publisher.calls != 2 || len(publisher.events) != 1 || publisher.events[0].Level != sharedlogging.LevelInfo {
		t.Fatalf("calls=%d events=%d", publisher.calls, len(publisher.events))
	}
}

func TestFallbackStoreQuarantinesCorruptPriorityAndReplaysRegular(t *testing.T) {
	root := filepath.Join(t.TempDir(), "spool")
	store := newStore(t, Config{RootDir: root, ReplayBatchSize: 1})
	if err := store.WriteBatch(context.Background(), []sharedlogging.Event{
		validEvent(t, sharedlogging.LevelInfo, "regular"),
		validEvent(t, sharedlogging.LevelError, "priority"),
	}); err != nil {
		t.Fatal(err)
	}
	priorityPaths, err := store.committedSegmentPaths(highPriority)
	if err != nil {
		t.Fatal(err)
	}
	if len(priorityPaths) != 1 {
		t.Fatalf("priority paths = %v", priorityPaths)
	}
	corrupt, err := os.ReadFile(priorityPaths[0])
	if err != nil {
		t.Fatal(err)
	}
	for index := range corrupt {
		corrupt[index] = 'x'
	}
	corrupt[len(corrupt)-1] = '\n'
	if err := os.WriteFile(priorityPaths[0], corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	publisher := &recordingPublisher{}
	if err := store.ReplayOnce(context.Background(), publisher); err != nil {
		t.Fatal(err)
	}
	if len(publisher.events) != 1 || publisher.events[0].Level != sharedlogging.LevelInfo {
		t.Fatalf("events = %#v", publisher.events)
	}
	if store.QuarantineBytes() == 0 {
		t.Fatal("quarantine bytes = 0")
	}
	entries, err := os.ReadDir(filepath.Join(root, quarantineDirectory))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("quarantine entries = %#v", entries)
	}
}

func TestFallbackStoreQuarantinesPermanentPublishFailure(t *testing.T) {
	permanentErr := errors.New("message too large")
	store := newStore(t, Config{
		RootDir: filepath.Join(t.TempDir(), "spool"), ReplayBatchSize: 1,
		IsPermanentPublishError: func(err error) bool { return errors.Is(err, permanentErr) },
	})
	if err := store.WriteBatch(context.Background(), []sharedlogging.Event{
		validEvent(t, sharedlogging.LevelError, "priority"),
		validEvent(t, sharedlogging.LevelInfo, "regular"),
	}); err != nil {
		t.Fatal(err)
	}
	publisher := &failFirstPublisher{err: permanentErr}
	if err := store.ReplayOnce(context.Background(), publisher); err != nil {
		t.Fatal(err)
	}
	if publisher.calls != 2 || len(publisher.events) != 1 || store.QuarantineBytes() == 0 {
		t.Fatalf("calls=%d events=%d quarantine=%d", publisher.calls, len(publisher.events), store.QuarantineBytes())
	}
}

func TestFallbackStoreQuarantinesOnlyPermanentlyRejectedRecord(t *testing.T) {
	permanentErr := errors.New("message too large")
	root := filepath.Join(t.TempDir(), "spool")
	store := newStore(t, Config{
		RootDir: root, ReplayBatchSize: 3,
		IsPermanentPublishError: func(err error) bool { return errors.Is(err, permanentErr) },
	})
	if err := store.WriteBatch(context.Background(), []sharedlogging.Event{
		validEvent(t, sharedlogging.LevelInfo, "first"),
		validEvent(t, sharedlogging.LevelInfo, "reject"),
		validEvent(t, sharedlogging.LevelInfo, "last"),
	}); err != nil {
		t.Fatal(err)
	}
	publisher := &selectivePermanentPublisher{err: permanentErr}
	if err := store.ReplayOnce(context.Background(), publisher); err != nil {
		t.Fatal(err)
	}
	if len(publisher.events) != 2 || publisher.events[0].Metadata["payload"] != "first" ||
		publisher.events[1].Metadata["payload"] != "last" {
		t.Fatalf("published events = %#v", publisher.events)
	}
	regular, _ := store.Bytes()
	if regular != 0 || store.QuarantineBytes() == 0 {
		t.Fatalf("regular=%d quarantine=%d", regular, store.QuarantineBytes())
	}
	paths, err := segmentPaths(filepath.Join(root, quarantineDirectory))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 {
		t.Fatalf("quarantine paths = %v", paths)
	}
	payload, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	event, err := sharedlogging.DecodeEvent(bytes.TrimSpace(payload))
	if err != nil {
		t.Fatal(err)
	}
	if event.Metadata["payload"] != "reject" {
		t.Fatalf("quarantined event = %#v", event)
	}
}

func TestFallbackReplayPublishUsesConfiguredTimeout(t *testing.T) {
	store := newStore(t, Config{
		RootDir: filepath.Join(t.TempDir(), "spool"), PublishTimeout: 10 * time.Millisecond,
	})
	if err := store.WriteBatch(context.Background(), []sharedlogging.Event{
		validEvent(t, sharedlogging.LevelInfo, "regular"),
	}); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	err := store.ReplayOnce(context.Background(), contextPublisher{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ReplayOnce() error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("ReplayOnce() elapsed = %s", elapsed)
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
	staged := filepath.Join(root, stagingDirectory, "interrupted", regularPriority)
	if err := os.MkdirAll(staged, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staged, "partial"+segmentSuffix), []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	newStore(t, Config{RootDir: root})
	entries, err := os.ReadDir(filepath.Join(root, stagingDirectory))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("staging entries = %#v", entries)
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
