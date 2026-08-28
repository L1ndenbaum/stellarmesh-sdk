package kafka

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	segmentio "github.com/segmentio/kafka-go"
)

func TestCheckRejectsMissingConfiguration(t *testing.T) {
	publisher, createErr := NewPublisher(Config{})
	if createErr != nil {
		t.Fatal(createErr)
	}
	err := publisher.Check(context.Background())
	if err == nil || !strings.Contains(err.Error(), "brokers") {
		t.Fatalf("error = %v", err)
	}
}

func TestIsMessageTooLargeRecognizesKafkaErrors(t *testing.T) {
	if !IsMessageTooLarge(segmentio.MessageTooLargeError{}) ||
		!IsMessageTooLarge(errors.Join(errors.New("publish"), segmentio.MessageSizeTooLarge)) ||
		!IsMessageTooLarge(segmentio.WriteErrors{errors.New("unavailable"), segmentio.MessageSizeTooLarge}) ||
		IsMessageTooLarge(errors.New("unavailable")) {
		t.Fatal("IsMessageTooLarge() classification mismatch")
	}
}

func TestNewPublisherConfiguresMaximumBatchBytes(t *testing.T) {
	publisher, err := NewPublisher(Config{BatchBytes: 2 << 20, BatchTimeout: 250 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer publisher.Close()
	if publisher.writer.BatchBytes != 2<<20 {
		t.Fatalf("batch bytes = %d", publisher.writer.BatchBytes)
	}
	if publisher.writer.RequiredAcks != segmentio.RequireAll {
		t.Fatalf("required acks = %d", publisher.writer.RequiredAcks)
	}
	if _, ok := publisher.writer.Balancer.(*segmentio.Hash); !ok {
		t.Fatalf("balancer = %T", publisher.writer.Balancer)
	}
	if publisher.writer.BatchTimeout != 250*time.Millisecond {
		t.Fatalf("batch timeout = %s", publisher.writer.BatchTimeout)
	}
	if _, err := NewPublisher(Config{BatchBytes: -1}); err == nil {
		t.Fatal("NewPublisher() accepted negative batch bytes")
	}
}

func TestPublisherHonorsCanceledContextAndCloses(t *testing.T) {
	publisher, err := NewPublisher(Config{Brokers: []string{"127.0.0.1:1"}, Topic: "events"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = publisher.Publish(ctx, []Message{{Key: []byte("event"), Value: []byte("payload")}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Publish() error = %v", err)
	}
	if err := publisher.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestCheckTopicRejectsNilDialer(t *testing.T) {
	err := CheckTopic(context.Background(), nil, []string{"kafka:9092"}, "events")
	if err == nil || !strings.Contains(err.Error(), "dialer") {
		t.Fatalf("error = %v", err)
	}
}

func TestCheckTopicProbesBrokersConcurrentlyAndCancelsBlackhole(t *testing.T) {
	blackholeStarted := make(chan struct{})
	blackholeCanceled := make(chan struct{})
	healthyStarted := make(chan struct{})
	err := checkTopic(context.Background(), []string{"blackhole:9092", "healthy:9092"}, "events", func(ctx context.Context, broker, _ string) error {
		switch broker {
		case "blackhole:9092":
			close(blackholeStarted)
			<-ctx.Done()
			close(blackholeCanceled)
			return ctx.Err()
		case "healthy:9092":
			close(healthyStarted)
			return nil
		default:
			return errors.New("unexpected broker")
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, signal := range map[string]<-chan struct{}{
		"blackhole start": blackholeStarted, "healthy start": healthyStarted, "blackhole cancel": blackholeCanceled,
	} {
		select {
		case <-signal:
		case <-time.After(time.Second):
			t.Fatalf("missing %s signal", name)
		}
	}
}

func TestCheckTopicAggregatesFailuresAndDeduplicatesBrokers(t *testing.T) {
	var mu sync.Mutex
	calls := make(map[string]int)
	err := checkTopic(context.Background(), []string{" second:9092 ", "first:9092", "second:9092", ""}, "events", func(_ context.Context, broker, _ string) error {
		mu.Lock()
		calls[broker]++
		mu.Unlock()
		return errors.New("unavailable")
	})
	if err == nil || !strings.Contains(err.Error(), "first:9092") || !strings.Contains(err.Error(), "second:9092") {
		t.Fatalf("error = %v", err)
	}
	if calls["first:9092"] != 1 || calls["second:9092"] != 1 {
		t.Fatalf("calls = %#v", calls)
	}
}

func TestCheckTopicReturnsCallerContextError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err := checkTopic(ctx, []string{"first:9092", "second:9092"}, "events", func(ctx context.Context, _, _ string) error {
		<-ctx.Done()
		return ctx.Err()
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v", err)
	}
}

func TestCheckTopicRejectsEmptyNormalizedConfiguration(t *testing.T) {
	if err := checkTopic(context.Background(), []string{" ", ""}, "events", func(context.Context, string, string) error { return nil }); err == nil {
		t.Fatal("checkTopic() accepted empty brokers")
	}
	if err := checkTopic(context.Background(), []string{"kafka:9092"}, " ", func(context.Context, string, string) error { return nil }); err == nil {
		t.Fatal("checkTopic() accepted empty topic")
	}
}
