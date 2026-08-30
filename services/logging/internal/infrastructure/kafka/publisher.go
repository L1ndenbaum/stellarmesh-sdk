// Package kafka 将日志事件适配到共享 Kafka 发布器。
package kafka

import (
	"context"
	"encoding/json"
	"errors"

	sharedlogging "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging"
	sharedkafka "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/mq/kafka"
)

var ErrMessageTooLarge = errors.New("logging Kafka message exceeds the contract size limit")

// Publisher 序列化规范日志事件。
type Publisher struct {
	base *sharedkafka.Publisher
}

// NewPublisher 创建日志事件发布器。
func NewPublisher(brokers []string, topic string, connection sharedkafka.ConnectionConfig) (*Publisher, error) {
	base, err := sharedkafka.NewPublisher(sharedkafka.Config{
		Brokers: brokers, Topic: topic, BatchBytes: sharedlogging.MaxKafkaMessageBytesV2, Connection: connection,
	})
	if err != nil {
		return nil, err
	}
	return &Publisher{base: base}, nil
}

// Check 校验 broker 和 topic 可用性。
func (publisher *Publisher) Check(ctx context.Context) error {
	return publisher.base.Check(ctx)
}

// Publish 序列化并发送事件。
func (publisher *Publisher) Publish(ctx context.Context, events []sharedlogging.Event) error {
	messages := make([]sharedkafka.Message, 0, len(events))
	for _, event := range events {
		payload, err := json.Marshal(event)
		if err != nil {
			return err
		}
		key := sharedlogging.KafkaPartitionKeyV2(event)
		if !sharedlogging.FitsKafkaKeyValueBudgetV2(event, len(payload)) {
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

// Close 释放 Kafka writer。
func (publisher *Publisher) Close() error {
	return publisher.base.Close()
}
