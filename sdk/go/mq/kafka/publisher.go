// Package kafka 提供基于 kafka-go 且与传输细节解耦的发布门面。
package kafka

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
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
	configuredBroker := false
	for _, broker := range brokers {
		if strings.TrimSpace(broker) != "" {
			configuredBroker = true
			break
		}
	}
	if !configuredBroker {
		return errors.New("kafka brokers are required")
	}
	if strings.TrimSpace(topic) == "" {
		return errors.New("kafka topic is required")
	}
	if dialer == nil {
		return errors.New("Kafka dialer is required")
	}
	return checkTopic(ctx, brokers, topic, func(ctx context.Context, broker, topic string) error {
		return probeTopic(ctx, dialer, broker, topic)
	})
}

type topicProbe func(context.Context, string, string) error

type probeResult struct {
	broker string
	err    error
}

func checkTopic(ctx context.Context, brokers []string, topic string, probe topicProbe) error {
	unique := make([]string, 0, len(brokers))
	seen := make(map[string]struct{}, len(brokers))
	for _, broker := range brokers {
		broker = strings.TrimSpace(broker)
		if broker == "" {
			continue
		}
		if _, exists := seen[broker]; exists {
			continue
		}
		seen[broker] = struct{}{}
		unique = append(unique, broker)
	}
	if len(unique) == 0 {
		return errors.New("kafka brokers are required")
	}
	if strings.TrimSpace(topic) == "" {
		return errors.New("kafka topic is required")
	}
	if probe == nil {
		return errors.New("kafka topic probe is required")
	}
	probeCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan probeResult, len(unique))
	for _, broker := range unique {
		go func() {
			results <- probeResult{broker: broker, err: probe(probeCtx, broker, topic)}
		}()
	}
	failures := make([]probeResult, 0, len(unique))
	for range unique {
		if err := ctx.Err(); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case result := <-results:
			if result.err == nil {
				if err := ctx.Err(); err != nil {
					return err
				}
				return nil
			}
			failures = append(failures, result)
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	sort.Slice(failures, func(left, right int) bool { return failures[left].broker < failures[right].broker })
	messages := make([]string, 0, len(failures))
	for _, failure := range failures {
		messages = append(messages, fmt.Sprintf("%s: %v", failure.broker, failure.err))
	}
	return fmt.Errorf("kafka startup check failed for topic %q: %s", topic, strings.Join(messages, "; "))
}

func probeTopic(ctx context.Context, dialer *segmentio.Dialer, broker, topic string) error {
	conn, err := dialer.DialContext(ctx, "tcp", broker)
	if err != nil {
		return err
	}
	var closeOnce sync.Once
	var closeErr error
	closeConn := func() error {
		closeOnce.Do(func() { closeErr = conn.Close() })
		return closeErr
	}
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			_ = closeConn()
			return fmt.Errorf("set topic probe deadline: %w", err)
		}
	}
	stopClose := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = closeConn()
		case <-stopClose:
		}
	}()
	partitions, readErr := conn.ReadPartitions(topic)
	close(stopClose)
	finalCloseErr := closeConn()
	if readErr != nil {
		return fmt.Errorf("read topic %q partitions: %w", topic, readErr)
	}
	if finalCloseErr != nil {
		return fmt.Errorf("close topic probe: %w", finalCloseErr)
	}
	if len(partitions) == 0 {
		return fmt.Errorf("topic %q has no partitions", topic)
	}
	return nil
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
