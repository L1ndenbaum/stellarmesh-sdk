package infrastructure

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	sharedlogging "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging"
	sharedkafka "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/mq/kafka"
	"github.com/L1ndenbaum/stellarmesh-sdk/sinks/logging/clickhouse/internal/application"
	segmentio "github.com/segmentio/kafka-go"
)

// KafkaSource 将 kafka-go 拉取和提交适配到应用层边界。
type KafkaSource struct {
	reader *segmentio.Reader
}

// NewKafkaSource 基于 consumer group reader 创建消息源。
func NewKafkaSource(reader *segmentio.Reader) (*KafkaSource, error) {
	if reader == nil {
		return nil, errors.New("Kafka reader is required")
	}
	return &KafkaSource{reader: reader}, nil
}

// FetchMessage 返回包含稳定来源坐标且与传输细节解耦的副本。
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

// Commit 推进已处理批次中每条源消息的 offset。
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

// Close 释放 consumer group reader。
func (source *KafkaSource) Close() error {
	return source.reader.Close()
}

// DeadLetterPublisher 将规范 DLQ 记录序列化到 Kafka。
type DeadLetterPublisher struct {
	base *sharedkafka.Publisher
}

// NewDeadLetterPublisher 创建必需的 Kafka DLQ 发布器。
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

// Check 校验 DLQ topic 是否存在且可访问。
func (publisher *DeadLetterPublisher) Check(ctx context.Context) error {
	return publisher.base.Check(ctx)
}

// PublishDeadLetters 写入规范的拒绝消息记录。
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

// PublishOversizeDeadLetters 写入紧凑 v2 记录，不复制源内容。
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

// Close 释放 DLQ producer。
func (publisher *DeadLetterPublisher) Close() error {
	return publisher.base.Close()
}
