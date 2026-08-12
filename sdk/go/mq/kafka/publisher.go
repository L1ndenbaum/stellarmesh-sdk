// Package kafka 提供基于 kafka-go 且与传输细节解耦的发布门面。
package kafka

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	segmentio "github.com/segmentio/kafka-go"
)

// Config 控制 Kafka 发布器。
type Config struct {
	Brokers      []string
	Topic        string
	BatchTimeout time.Duration
	BatchBytes   int64
	Connection   ConnectionConfig
}

// Message 是 Publisher 接受的序列化消息。
type Message struct {
	Key   []byte
	Value []byte
	Time  time.Time
}

// Publisher 持有 kafka-go writer。
type Publisher struct {
	brokers   []string
	topic     string
	writer    *segmentio.Writer
	dialer    *segmentio.Dialer
	transport *segmentio.Transport
}

// IsMessageTooLarge 判断 kafka-go 或 broker 是否因大小原因拒绝序列化记录。
func IsMessageTooLarge(err error) bool {
	if err == nil {
		return false
	}
	var messageTooLarge segmentio.MessageTooLargeError
	if errors.As(err, &messageTooLarge) || errors.Is(err, segmentio.MessageSizeTooLarge) {
		return true
	}
	var writeErrors segmentio.WriteErrors
	if !errors.As(err, &writeErrors) {
		return false
	}
	for _, writeErr := range writeErrors {
		if IsMessageTooLarge(writeErr) {
			return true
		}
	}
	return false
}

// NewPublisher 创建使用哈希分区并要求全部同步副本确认的发布器。
func NewPublisher(cfg Config) (*Publisher, error) {
	if cfg.BatchBytes < 0 {
		return nil, errors.New("Kafka publisher batch bytes must not be negative")
	}
	connection, err := NewConnection(cfg.Connection)
	if err != nil {
		return nil, err
	}
	batchTimeout := cfg.BatchTimeout
	if batchTimeout <= 0 {
		batchTimeout = 100 * time.Millisecond
	}
	transport := connection.Transport()
	return &Publisher{
		brokers:   cfg.Brokers,
		topic:     cfg.Topic,
		dialer:    connection.Dialer(),
		transport: transport,
		writer: &segmentio.Writer{
			Addr:         segmentio.TCP(cfg.Brokers...),
			Topic:        cfg.Topic,
			RequiredAcks: segmentio.RequireAll,
			Balancer:     &segmentio.Hash{},
			BatchTimeout: batchTimeout,
			BatchBytes:   cfg.BatchBytes,
			Transport:    transport,
		},
	}, nil
}

// Check 校验配置的 broker 是否提供配置的 topic。
func (publisher *Publisher) Check(ctx context.Context) error {
	return CheckTopic(ctx, publisher.dialer, publisher.brokers, publisher.topic)
}

// CheckTopic 校验配置的 broker 是否提供指定的既有 topic。
func CheckTopic(ctx context.Context, dialer *segmentio.Dialer, brokers []string, topic string) error {
	if len(brokers) == 0 {
		return errors.New("kafka brokers are required")
	}
	if strings.TrimSpace(topic) == "" {
		return errors.New("kafka topic is required")
	}
	if dialer == nil {
		return errors.New("Kafka dialer is required")
	}
	var failures []string
	for _, broker := range brokers {
		broker = strings.TrimSpace(broker)
		if broker == "" {
			continue
		}
		conn, err := dialer.DialContext(ctx, "tcp", broker)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", broker, err))
			continue
		}
		partitions, readErr := conn.ReadPartitions(topic)
		closeErr := conn.Close()
		if readErr != nil {
			failures = append(failures, fmt.Sprintf("%s topic %q: %v", broker, topic, readErr))
			continue
		}
		if closeErr != nil {
			failures = append(failures, fmt.Sprintf("%s close: %v", broker, closeErr))
			continue
		}
		if len(partitions) > 0 {
			return nil
		}
		failures = append(failures, fmt.Sprintf("%s topic %q has no partitions", broker, topic))
	}
	if len(failures) == 0 {
		return errors.New("kafka brokers are required")
	}
	return fmt.Errorf("kafka startup check failed for topic %q: %s", topic, strings.Join(failures, "; "))
}

// Publish 将序列化消息写入 Kafka。
func (publisher *Publisher) Publish(ctx context.Context, messages []Message) error {
	if len(messages) == 0 {
		return nil
	}
	kafkaMessages := make([]segmentio.Message, 0, len(messages))
	for _, message := range messages {
		kafkaMessages = append(kafkaMessages, segmentio.Message{Key: message.Key, Value: message.Value, Time: message.Time})
	}
	return publisher.writer.WriteMessages(ctx, kafkaMessages...)
}

// Close 释放 Kafka writer。
func (publisher *Publisher) Close() error {
	err := publisher.writer.Close()
	publisher.transport.CloseIdleConnections()
	return err
}
