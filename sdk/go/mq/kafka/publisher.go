// Package kafka provides a transport-neutral publisher facade around kafka-go.
package kafka

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	segmentio "github.com/segmentio/kafka-go"
)

// Config controls a Kafka publisher.
type Config struct {
	Brokers      []string
	Topic        string
	BatchTimeout time.Duration
	Connection   ConnectionConfig
}

// Message is the serialized message accepted by Publisher.
type Message struct {
	Key   []byte
	Value []byte
	Time  time.Time
}

// Publisher owns a kafka-go writer.
type Publisher struct {
	brokers   []string
	topic     string
	writer    *segmentio.Writer
	dialer    *segmentio.Dialer
	transport *segmentio.Transport
}

// NewPublisher creates a publisher using hash partitioning and leader acknowledgement.
func NewPublisher(cfg Config) (*Publisher, error) {
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
			RequiredAcks: segmentio.RequireOne,
			Balancer:     &segmentio.Hash{},
			BatchTimeout: batchTimeout,
			Transport:    transport,
		},
	}, nil
}

// Check verifies that a configured broker exposes the configured topic.
func (publisher *Publisher) Check(ctx context.Context) error {
	if len(publisher.brokers) == 0 {
		return errors.New("kafka brokers are required")
	}
	if strings.TrimSpace(publisher.topic) == "" {
		return errors.New("kafka topic is required")
	}
	var failures []string
	for _, broker := range publisher.brokers {
		broker = strings.TrimSpace(broker)
		if broker == "" {
			continue
		}
		conn, err := publisher.dialer.DialContext(ctx, "tcp", broker)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", broker, err))
			continue
		}
		partitions, readErr := conn.ReadPartitions(publisher.topic)
		closeErr := conn.Close()
		if readErr != nil {
			failures = append(failures, fmt.Sprintf("%s topic %q: %v", broker, publisher.topic, readErr))
			continue
		}
		if closeErr != nil {
			failures = append(failures, fmt.Sprintf("%s close: %v", broker, closeErr))
			continue
		}
		if len(partitions) > 0 {
			return nil
		}
		failures = append(failures, fmt.Sprintf("%s topic %q has no partitions", broker, publisher.topic))
	}
	if len(failures) == 0 {
		return errors.New("kafka brokers are required")
	}
	return fmt.Errorf("kafka startup check failed for topic %q: %s", publisher.topic, strings.Join(failures, "; "))
}

// Publish writes serialized messages to Kafka.
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

// Close releases the Kafka writer.
func (publisher *Publisher) Close() error {
	err := publisher.writer.Close()
	publisher.transport.CloseIdleConnections()
	return err
}
