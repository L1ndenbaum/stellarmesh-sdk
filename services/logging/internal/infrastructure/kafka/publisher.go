// Package kafka adapts logging events to the shared Kafka publisher.
package kafka

import (
	"context"
	"encoding/json"
	"errors"

	sharedlogging "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging"
	sharedkafka "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/mq/kafka"
)

var ErrMessageTooLarge = errors.New("logging Kafka message exceeds the contract size limit")

// Publisher serializes canonical logging events.
type Publisher struct {
	base *sharedkafka.Publisher
}

// NewPublisher creates a logging event publisher.
func NewPublisher(brokers []string, topic string, connection sharedkafka.ConnectionConfig) (*Publisher, error) {
	base, err := sharedkafka.NewPublisher(sharedkafka.Config{
		Brokers: brokers, Topic: topic, BatchBytes: sharedlogging.MaxKafkaMessageBytesV1, Connection: connection,
	})
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
		key := sharedlogging.KafkaPartitionKeyV1(event)
		if !sharedlogging.FitsKafkaKeyValueBudgetV1(event, len(payload)) {
			return ErrMessageTooLarge
		}
		messages = append(messages, sharedkafka.Message{Key: key, Value: payload, Time: event.Timestamp})
	}
	err := publisher.base.Publish(ctx, messages)
	if sharedkafka.IsMessageTooLarge(err) {
		return errors.Join(ErrMessageTooLarge, err)
	}
	return err
}

// Close releases the Kafka writer.
func (publisher *Publisher) Close() error {
	return publisher.base.Close()
}
