package infrastructure

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	sharedlogging "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging"
	sharedkafka "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/mq/kafka"
	"github.com/L1ndenbaum/stellarmesh-sdk/sinks/clickhouse/internal/application"
	segmentio "github.com/segmentio/kafka-go"
)

// KafkaSource adapts kafka-go fetches and commits to the application boundary.
type KafkaSource struct {
	reader *segmentio.Reader
}

// NewKafkaSource creates a source over one consumer-group reader.
func NewKafkaSource(reader *segmentio.Reader) (*KafkaSource, error) {
	if reader == nil {
		return nil, errors.New("Kafka reader is required")
	}
	return &KafkaSource{reader: reader}, nil
}

// FetchMessage returns a transport-neutral copy with stable source coordinates.
func (source *KafkaSource) FetchMessage(ctx context.Context) (application.Message, error) {
	message, err := source.reader.FetchMessage(ctx)
	if err != nil {
		return application.Message{}, err
	}
	return application.Message{
		Topic: message.Topic, Partition: message.Partition, Offset: message.Offset,
		Timestamp: message.Time, Key: append([]byte(nil), message.Key...),
		Value: append([]byte(nil), message.Value...), Handle: message,
	}, nil
}

// Commit advances offsets for every source message in the processed batch.
func (source *KafkaSource) Commit(ctx context.Context, messages []application.Message) error {
	kafkaMessages := make([]segmentio.Message, 0, len(messages))
	for _, message := range messages {
		kafkaMessage, ok := message.Handle.(segmentio.Message)
		if !ok {
			return fmt.Errorf("unsupported Kafka offset handle %T", message.Handle)
		}
		kafkaMessages = append(kafkaMessages, kafkaMessage)
	}
	if len(kafkaMessages) == 0 {
		return nil
	}
	return source.reader.CommitMessages(ctx, kafkaMessages...)
}

// Close releases the consumer-group reader.
func (source *KafkaSource) Close() error {
	return source.reader.Close()
}

// DeadLetterPublisher serializes canonical DLQ records to Kafka.
type DeadLetterPublisher struct {
	base *sharedkafka.Publisher
}

// NewDeadLetterPublisher creates a required Kafka DLQ publisher.
func NewDeadLetterPublisher(
	brokers []string,
	topic string,
	connection sharedkafka.ConnectionConfig,
	batchBytes int64,
) (*DeadLetterPublisher, error) {
	base, err := sharedkafka.NewPublisher(sharedkafka.Config{
		Brokers: brokers, Topic: topic, BatchBytes: batchBytes, Connection: connection,
	})
	if err != nil {
		return nil, err
	}
	return &DeadLetterPublisher{base: base}, nil
}

// Check verifies that the DLQ topic exists and is accessible.
func (publisher *DeadLetterPublisher) Check(ctx context.Context) error {
	return publisher.base.Check(ctx)
}

// PublishDeadLetters writes canonical rejected-message records.
func (publisher *DeadLetterPublisher) PublishDeadLetters(
	ctx context.Context,
	records []sharedlogging.DeadLetter,
) error {
	messages := make([]sharedkafka.Message, 0, len(records))
	for _, record := range records {
		if err := record.Validate(); err != nil {
			return err
		}
		payload, err := json.Marshal(record)
		if err != nil {
			return err
		}
		key := []byte(fmt.Sprintf("%s/%d/%d", record.SourceTopic, record.SourcePartition, record.SourceOffset))
		messages = append(messages, sharedkafka.Message{Key: key, Value: payload, Time: record.FailedAt})
	}
	return publisher.base.Publish(ctx, messages)
}

// PublishOversizeDeadLetters writes compact v2 records without copying source content.
func (publisher *DeadLetterPublisher) PublishOversizeDeadLetters(
	ctx context.Context,
	records []sharedlogging.OversizeDeadLetter,
) error {
	messages := make([]sharedkafka.Message, 0, len(records))
	for _, record := range records {
		if err := record.Validate(); err != nil {
			return err
		}
		payload, err := json.Marshal(record)
		if err != nil {
			return err
		}
		key := []byte(fmt.Sprintf("%s/%d/%d", record.SourceTopic, record.SourcePartition, record.SourceOffset))
		messages = append(messages, sharedkafka.Message{Key: key, Value: payload, Time: record.FailedAt})
	}
	return publisher.base.Publish(ctx, messages)
}

// Close releases the DLQ producer.
func (publisher *DeadLetterPublisher) Close() error {
	return publisher.base.Close()
}
