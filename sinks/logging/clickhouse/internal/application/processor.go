// Package application 协调 Kafka 解码、ClickHouse 写入、死信处理和 offset 提交。
package application

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	sharedlogging "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging"
)

const maxDeadLetterErrorRunes = 2048

var (
	// ErrClickHouseInsert 标识可重试的 ClickHouse 阶段故障。
	ErrClickHouseInsert = errors.New("ClickHouse insertion failed")
	// ErrDeadLetterPublish 标识可重试的 DLQ 发布故障。
	ErrDeadLetterPublish = errors.New("dead-letter publication failed")
	// ErrOffsetCommit 标识可重试的源 offset 提交故障。
	ErrOffsetCommit = errors.New("Kafka offset commit failed")
)

// Message 包含 Kafka 载荷、稳定来源坐标和 offset handle。
type Message struct {
	Topic     string
	Partition int
	Offset    int64
	Timestamp time.Time
	Key       []byte
	Value     []byte
	Handle    any
}

// Inserter 持久化已解码事件。
type Inserter interface {
	InsertEvents(context.Context, []sharedlogging.Event) error
}

// DeadLetterPublisher 将被拒绝的源消息持久化到配置的 DLQ。
type DeadLetterPublisher interface {
	PublishDeadLetters(context.Context, []sharedlogging.DeadLetter) error
	PublishOversizeDeadLetters(context.Context, []sharedlogging.OversizeDeadLetter) error
}

// Committer 在所有持久写入成功后推进 Kafka offset。
type Committer interface {
	Commit(context.Context, []Message) error
}

// Observer 接收有界 sink 指标和就绪状态变化。
type Observer interface {
	SetReady(bool)
	SetPendingMessages(int)
	SetPendingBytes(int64)
	ObserveMessages(result string, count int)
	ObserveOperation(operation, result string)
}

// ProcessorConfig 提供所有持久处理阶段及其时钟。
type ProcessorConfig struct {
	Inserter              Inserter
	DeadLetters           DeadLetterPublisher
	Committer             Committer
	Observer              Observer
	Now                   func() time.Time
	MaxSourceMessageBytes int64
}

// Processor 管理写入、死信和提交顺序。
type Processor struct {
	inserter              Inserter
	deadLetters           DeadLetterPublisher
	committer             Committer
	observer              Observer
	now                   func() time.Time
	maxSourceMessageBytes int64
}

// NewProcessor 校验 sink 所需的持久处理阶段。
func NewProcessor(config ProcessorConfig) (*Processor, error) {
	if config.Inserter == nil {
		return nil, errors.New("ClickHouse inserter is required")
	}
	if config.DeadLetters == nil {
		return nil, errors.New("dead-letter publisher is required")
	}
	if config.Committer == nil {
		return nil, errors.New("Kafka committer is required")
	}
	if config.MaxSourceMessageBytes <= 0 {
		return nil, errors.New("maximum source message bytes must be positive")
	}
	now := config.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Processor{
		inserter: config.Inserter, deadLetters: config.DeadLetters,
		committer: config.Committer, observer: config.Observer, now: now,
		maxSourceMessageBytes: config.MaxSourceMessageBytes,
	}, nil
}

// ProcessBatch 写入有效事件、发布被拒绝消息，然后提交所有源 offset。
func (processor *Processor) ProcessBatch(ctx context.Context, messages []Message) error {
	if len(messages) == 0 {
		return nil
	}
	events := make([]sharedlogging.Event, 0, len(messages))
	deadLetters := make([]sharedlogging.DeadLetter, 0)
	oversizeDeadLetters := make([]sharedlogging.OversizeDeadLetter, 0)
	for _, message := range messages {
		if sourceMessageBytes(message) > processor.maxSourceMessageBytes {
			deadLetter, buildErr := processor.newOversizeDeadLetter(message)
			if buildErr != nil {
				processor.observeOperation("dead_letter_v2_build", "failed")
				return fmt.Errorf("%w: %w", ErrDeadLetterPublish, buildErr)
			}
			oversizeDeadLetters = append(oversizeDeadLetters, deadLetter)
			continue
		}
		event, err := sharedlogging.DecodeEvent(message.Value)
		if err != nil {
			deadLetter, buildErr := processor.newDeadLetter(message, err)
			if buildErr != nil {
				processor.observeOperation("dead_letter_build", "failed")
				return fmt.Errorf("%w: %w", ErrDeadLetterPublish, buildErr)
			}
			deadLetters = append(deadLetters, deadLetter)
			continue
		}
		events = append(events, event)
	}

	if len(events) > 0 {
		if err := processor.inserter.InsertEvents(ctx, events); err != nil {
			processor.observeOperation("clickhouse_insert", "failed")
			return fmt.Errorf("%w: %w", ErrClickHouseInsert, err)
		}
		processor.observeOperation("clickhouse_insert", "success")
	}
	if len(deadLetters) > 0 {
		if err := processor.deadLetters.PublishDeadLetters(ctx, deadLetters); err != nil {
			processor.observeOperation("dead_letter_publish", "failed")
			return fmt.Errorf("%w: %w", ErrDeadLetterPublish, err)
		}
		processor.observeOperation("dead_letter_publish", "success")
	}
	if len(oversizeDeadLetters) > 0 {
		if err := processor.deadLetters.PublishOversizeDeadLetters(ctx, oversizeDeadLetters); err != nil {
			processor.observeOperation("dead_letter_v2_publish", "failed")
			return fmt.Errorf("%w: %w", ErrDeadLetterPublish, err)
		}
		processor.observeOperation("dead_letter_v2_publish", "success")
	}
	if err := processor.committer.Commit(ctx, messages); err != nil {
		processor.observeOperation("offset_commit", "failed")
		return fmt.Errorf("%w: %w", ErrOffsetCommit, err)
	}
	processor.observeOperation("offset_commit", "success")
	processor.observeMessages("inserted", len(events))
	processor.observeMessages("dead_lettered", len(deadLetters)+len(oversizeDeadLetters))
	processor.observeMessages("oversized", len(oversizeDeadLetters))
	return nil
}

func (processor *Processor) newOversizeDeadLetter(message Message) (sharedlogging.OversizeDeadLetter, error) {
	failedAt := processor.now().UTC()
	var sourceTimestamp *time.Time
	if !message.Timestamp.IsZero() {
		value := message.Timestamp.UTC()
		sourceTimestamp = &value
	}
	keyHash := sha256.Sum256(message.Key)
	payloadHash := sha256.Sum256(message.Value)
	deadLetter := sharedlogging.OversizeDeadLetter{
		SchemaVersion: sharedlogging.DeadLetterSchemaV2, SourceTopic: message.Topic,
		SourcePartition: message.Partition, SourceOffset: message.Offset, SourceTimestamp: sourceTimestamp,
		Reason: "source_message_too_large",
		Error: fmt.Sprintf(
			"Kafka source message is %d bytes and exceeds the %d byte limit",
			sourceMessageBytes(message), processor.maxSourceMessageBytes,
		),
		SourceKeyBytes: int64(len(message.Key)), SourceKeySHA256: hex.EncodeToString(keyHash[:]),
		PayloadBytes: int64(len(message.Value)), PayloadSHA256: hex.EncodeToString(payloadHash[:]),
		ContentOmitted: true, FailedAt: failedAt,
	}
	if err := deadLetter.Validate(); err != nil {
		return sharedlogging.OversizeDeadLetter{}, err
	}
	return deadLetter, nil
}

func sourceMessageBytes(message Message) int64 {
	return int64(len(message.Key)) + int64(len(message.Value))
}

func (processor *Processor) newDeadLetter(message Message, decodeErr error) (sharedlogging.DeadLetter, error) {
	failedAt := processor.now().UTC()
	var sourceTimestamp *time.Time
	if !message.Timestamp.IsZero() {
		value := message.Timestamp.UTC()
		sourceTimestamp = &value
	}
	deadLetter := sharedlogging.DeadLetter{
		SchemaVersion: sharedlogging.DeadLetterSchemaV1, SourceTopic: message.Topic,
		SourcePartition: message.Partition, SourceOffset: message.Offset, SourceTimestamp: sourceTimestamp,
		SourceKeyBase64: base64.StdEncoding.EncodeToString(message.Key), Reason: "invalid_event",
		Error:         truncateRunes(decodeErr.Error(), maxDeadLetterErrorRunes),
		PayloadBase64: base64.StdEncoding.EncodeToString(message.Value), FailedAt: failedAt,
	}
	if err := deadLetter.Validate(); err != nil {
		return sharedlogging.DeadLetter{}, err
	}
	return deadLetter, nil
}

func truncateRunes(value string, limit int) string {
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit])
}

func (processor *Processor) observeOperation(operation, result string) {
	if processor.observer != nil {
		processor.observer.ObserveOperation(operation, result)
	}
}

func (processor *Processor) observeMessages(result string, count int) {
	if processor.observer != nil && count > 0 {
		processor.observer.ObserveMessages(result, count)
	}
}
