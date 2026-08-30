package console

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"sync"
	"testing"
	"time"

	sharedlogging "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging"
)

type recordingObserver struct {
	mu      sync.Mutex
	results map[string]int
}

func (observer *recordingObserver) ObserveConsole(result string, count int) {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	if observer.results == nil {
		observer.results = make(map[string]int)
	}
	observer.results[result] += count
}

type blockingWriter struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (writer *blockingWriter) Write(payload []byte) (int, error) {
	writer.once.Do(func() { close(writer.started) })
	<-writer.release
	return len(payload), nil
}

func TestSinkWritesDecodableSingleLineJSON(t *testing.T) {
	var output bytes.Buffer
	observer := &recordingObserver{}
	sink, err := NewSink(&output, observer)
	if err != nil {
		t.Fatal(err)
	}
	event := sharedlogging.Event{
		EventID: "018f16b6-3f9f-7d98-a328-3eac70bd0542", Timestamp: time.Now(),
		Kind: sharedlogging.EventKindAudit, Level: sharedlogging.LevelInfo,
		Service: "backend", Message: "first\n\x1b[31msecond", TraceID: "trace-1", Metadata: map[string]any{},
	}
	if err := sink.WriteBatch(context.Background(), []sharedlogging.Event{event}); err != nil {
		t.Fatal(err)
	}
	if err := sink.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if bytes.Count(output.Bytes(), []byte{'\n'}) != 1 {
		t.Fatalf("output contains multiple physical lines: %q", output.String())
	}
	var decoded sharedlogging.Event
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Kind != sharedlogging.EventKindAudit || decoded.Level != sharedlogging.LevelInfo ||
		decoded.Message != event.Message || decoded.TraceID != event.TraceID {
		t.Fatalf("decoded event = %#v", decoded)
	}
}

func TestSinkNeverBlocksAndBoundsQueuedBytes(t *testing.T) {
	writer := &blockingWriter{started: make(chan struct{}), release: make(chan struct{})}
	observer := &recordingObserver{}
	sink, err := newSink(writer, observer, 1, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	event := sharedlogging.Event{Kind: sharedlogging.EventKindLog, Level: sharedlogging.LevelInfo, Service: "backend", Message: "event", Metadata: map[string]any{}}
	if err := sink.WriteBatch(context.Background(), []sharedlogging.Event{event}); err != nil {
		t.Fatal(err)
	}
	<-writer.started
	returned := make(chan struct{})
	go func() {
		_ = sink.WriteBatch(context.Background(), []sharedlogging.Event{event})
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("WriteBatch() blocked behind stdout")
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := sink.Close(shutdownCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close() error = %v", err)
	}
	close(writer.release)
	if err := sink.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	observer.mu.Lock()
	defer observer.mu.Unlock()
	if observer.results["emitted"] != 1 || observer.results["dropped"] != 1 {
		t.Fatalf("results = %#v", observer.results)
	}
}

func TestSinkReportsEncodingFailureWithoutReturningError(t *testing.T) {
	observer := &recordingObserver{}
	sink, err := NewSink(&bytes.Buffer{}, observer)
	if err != nil {
		t.Fatal(err)
	}
	event := sharedlogging.Event{Metadata: map[string]any{"invalid": math.NaN()}}
	if err := sink.WriteBatch(context.Background(), []sharedlogging.Event{event}); err != nil {
		t.Fatal(err)
	}
	if err := sink.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	observer.mu.Lock()
	defer observer.mu.Unlock()
	if observer.results["failed"] != 1 {
		t.Fatalf("results = %#v", observer.results)
	}
}
