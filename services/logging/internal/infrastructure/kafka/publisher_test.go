package kafka

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	sharedlogging "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging"
)

func TestPublisherRejectsOversizedMessageBeforeKafkaWrite(t *testing.T) {
	publisher := &Publisher{}
	event := sharedlogging.Event{
		EventID:   "018f16b6-3f9f-7d98-a328-3eac70bd0542",
		Timestamp: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC),
		Kind:      sharedlogging.EventKindLog, Level: sharedlogging.LevelInfo, Service: "test",
		Message: strings.Repeat("x", sharedlogging.MaxKafkaMessageBytesV2), Metadata: map[string]any{},
	}
	if err := publisher.Publish(context.Background(), []sharedlogging.Event{event}); !errors.Is(err, ErrMessageTooLarge) {
		t.Fatalf("Publish() error = %v", err)
	}
}

func TestPublisherUsesBoundedTracePartitionKey(t *testing.T) {
	event := sharedlogging.Event{
		EventID: "018f16b6-3f9f-7d98-a328-3eac70bd0542", TraceID: strings.Repeat("trace", 1000),
	}
	key := sharedlogging.KafkaPartitionKeyV2(event)
	if len(key) != 32 || !sharedlogging.FitsKafkaKeyValueBudgetV2(event, sharedlogging.MaxEventJSONBytesV2) {
		t.Fatalf("key length=%d", len(key))
	}
}
