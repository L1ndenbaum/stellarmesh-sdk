// Package application coordinates Kafka decoding, ClickHouse insertion, and offset commit.
package application

import (
	"context"
	"encoding/json"

	sharedlogging "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging"
)

// Message contains a Kafka payload and transport-specific offset handle.
type Message struct {
	Value  []byte
	Handle any
}

// Inserter persists decoded events.
type Inserter interface {
	InsertEvents(context.Context, []sharedlogging.Event) error
}

// Committer advances Kafka offsets after successful insertion.
type Committer interface {
	Commit(context.Context, []Message) error
}

// ProcessBatch validates, inserts, and then commits a Kafka batch.
func ProcessBatch(ctx context.Context, messages []Message, inserter Inserter, committer Committer) error {
	if len(messages) == 0 {
		return nil
	}
	events := make([]sharedlogging.Event, 0, len(messages))
	for _, message := range messages {
		var event sharedlogging.Event
		if err := json.Unmarshal(message.Value, &event); err != nil {
			return err
		}
		if err := event.Validate(); err != nil {
			return err
		}
		events = append(events, event)
	}
	if err := inserter.InsertEvents(ctx, events); err != nil {
		return err
	}
	return committer.Commit(ctx, messages)
}
