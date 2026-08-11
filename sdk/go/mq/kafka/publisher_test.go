package kafka

import (
	"context"
	"strings"
	"testing"

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

func TestNewPublisherConfiguresMaximumBatchBytes(t *testing.T) {
	publisher, err := NewPublisher(Config{BatchBytes: 2 << 20})
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
	if _, err := NewPublisher(Config{BatchBytes: -1}); err == nil {
		t.Fatal("NewPublisher() accepted negative batch bytes")
	}
}

func TestCheckTopicRejectsNilDialer(t *testing.T) {
	err := CheckTopic(context.Background(), nil, []string{"kafka:9092"}, "events")
	if err == nil || !strings.Contains(err.Error(), "dialer") {
		t.Fatalf("error = %v", err)
	}
}
