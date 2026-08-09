// Package kafka adapts logging events to the shared Kafka publisher.
package kafka

import (
	"context"
	"encoding/json"

	sharedlogging "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging"
	sharedkafka "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/mq/kafka"
)

// Publisher serializes canonical logging events.
type Publisher struct {
	base *sharedkafka.Publisher
}

// NewPublisher creates a logging event publisher.
func NewPublisher(brokers []string, topic string, connection sharedkafka.ConnectionConfig) (*Publisher, error) {
	base, err := sharedkafka.NewPublisher(sharedkafka.Config{Brokers: brokers, Topic: topic, Connection: connection})
	if err != nil {
		return nil, err
	}
	return &Publisher{base: base}, nil
}

// Check verifies broker and topic availability.
func (publisher *Publisher) Check(ctx context.Context) error {
	return publisher.base.Check(ctx)
}

// Publish serializes and sends events.
func (publisher *Publisher) Publish(ctx context.Context, events []sharedlogging.Event) error {
	messages := make([]sharedkafka.Message, 0, len(events))
	for _, event := range events {
		payload, err := json.Marshal(event)
		if err != nil {
			return err
		}
		key := []byte(event.TraceID)
		if len(key) == 0 {
			key = []byte(event.EventID)
		}
		messages = append(messages, sharedkafka.Message{Key: key, Value: payload, Time: event.Timestamp})
	}
	return publisher.base.Publish(ctx, messages)
}

// Close releases the Kafka writer.
func (publisher *Publisher) Close() error {
	return publisher.base.Close()
}
