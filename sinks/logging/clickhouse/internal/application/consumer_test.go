package application

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type channelSource struct {
	messages chan Message
}

func (source *channelSource) FetchMessage(ctx context.Context) (Message, error) {
	select {
	case message := <-source.messages:
		return message, nil
	case <-ctx.Done():
		return Message{}, ctx.Err()
	}
}

type retryingProcessor struct {
	mu       sync.Mutex
	failures int
	calls    [][]Message
}

func (processor *retryingProcessor) ProcessBatch(_ context.Context, messages []Message) error {
	processor.mu.Lock()
	defer processor.mu.Unlock()
	processor.calls = append(processor.calls, append([]Message(nil), messages...))
	if processor.failures > 0 {
		processor.failures--
		return errors.New("temporary failure")
	}
	return nil
}

func TestRunRetriesSameBatchAndDrainsOnShutdown(t *testing.T) {
	source := &channelSource{messages: make(chan Message, 2)}
	source.messages <- Message{Offset: 1}
	source.messages <- Message{Offset: 2}
	processor := &retryingProcessor{failures: 1}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, source, processor, ConsumerConfig{
			BatchSize: 2, RetryInterval: time.Millisecond, ShutdownTimeout: time.Second,
		})
	}()

	deadline := time.Now().Add(time.Second)
	for {
		processor.mu.Lock()
		calls := len(processor.calls)
		processor.mu.Unlock()
		if calls >= 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	processor.mu.Lock()
	defer processor.mu.Unlock()
	if len(processor.calls) != 2 || len(processor.calls[0]) != 2 || len(processor.calls[1]) != 2 {
		t.Fatalf("calls = %#v", processor.calls)
	}
}

func TestRunSplitsBatchesBySourceBytes(t *testing.T) {
	source := &channelSource{messages: make(chan Message, 3)}
	for offset := int64(1); offset <= 3; offset++ {
		source.messages <- Message{Offset: offset, Value: []byte("abc")}
	}
	processor := &retryingProcessor{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, source, processor, ConsumerConfig{
			BatchSize: 10, BatchMaxBytes: 4, FlushInterval: time.Millisecond,
			RetryInterval: time.Millisecond, ShutdownTimeout: time.Second,
		})
	}()

	deadline := time.Now().Add(time.Second)
	for {
		processor.mu.Lock()
		calls := len(processor.calls)
		processor.mu.Unlock()
		if calls >= 3 || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	processor.mu.Lock()
	defer processor.mu.Unlock()
	if len(processor.calls) != 3 {
		t.Fatalf("calls = %#v", processor.calls)
	}
	for _, batch := range processor.calls {
		if len(batch) != 1 {
			t.Fatalf("batch = %#v", batch)
		}
	}
}
