// Package config loads ClickHouse sink settings.
package config

import (
	"errors"
	"strings"
	"time"

	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/envconfig"
	sharedlogging "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging"
	sharedkafka "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/mq/kafka"
)

// Config contains Kafka consumer and ClickHouse writer settings.
type Config struct {
	KafkaBrokers          []string
	KafkaTopic            string
	KafkaDLQTopic         string
	KafkaGroupID          string
	KafkaConnection       sharedkafka.ConnectionConfig
	MaxSourceMessageBytes int64
	ClickHouseHTTPURL     string
	ClickHouseDatabase    string
	ClickHouseUser        string
	ClickHousePassword    string
	BatchSize             int
	BatchMaxBytes         int64
	FlushInterval         time.Duration
	RetryInterval         time.Duration
	ShutdownTimeout       time.Duration
	HTTPTimeout           time.Duration
	ObservabilityAddr     string
}

// Load reads canonical STELLARMESH_LOGGING_* environment variables.
func Load() (Config, error) {
	cfg := Config{
		KafkaBrokers:          envconfig.CSV("STELLARMESH_LOGGING_KAFKA_BROKERS", "kafka:9092"),
		KafkaTopic:            envconfig.String("STELLARMESH_LOGGING_KAFKA_TOPIC", sharedlogging.TopicV1),
		KafkaDLQTopic:         envconfig.String("STELLARMESH_LOGGING_KAFKA_DLQ_TOPIC", sharedlogging.DeadLetterTopicV1),
		KafkaGroupID:          envconfig.String("STELLARMESH_LOGGING_WRITER_GROUP_ID", "stellarmesh-logging-clickhouse"),
		KafkaConnection:       kafkaConnectionConfig(),
		MaxSourceMessageBytes: envconfig.ByteSize("STELLARMESH_LOGGING_WRITER_MAX_SOURCE_MESSAGE_BYTES", 1<<20),
		ClickHouseHTTPURL:     envconfig.String("STELLARMESH_LOGGING_CLICKHOUSE_HTTP_URL", "http://clickhouse:8123"),
		ClickHouseDatabase:    envconfig.String("STELLARMESH_LOGGING_CLICKHOUSE_DATABASE", ""),
		ClickHouseUser:        envconfig.String("STELLARMESH_LOGGING_CLICKHOUSE_USER", ""),
		ClickHousePassword:    envconfig.String("STELLARMESH_LOGGING_CLICKHOUSE_PASSWORD", ""),
		BatchSize:             envconfig.Int("STELLARMESH_LOGGING_WRITER_BATCH_SIZE", 500),
		BatchMaxBytes:         envconfig.ByteSize("STELLARMESH_LOGGING_WRITER_BATCH_MAX_BYTES", 16<<20),
		FlushInterval:         envconfig.Duration("STELLARMESH_LOGGING_WRITER_FLUSH_INTERVAL", time.Second),
		RetryInterval:         envconfig.Duration("STELLARMESH_LOGGING_WRITER_RETRY_INTERVAL", time.Second),
		ShutdownTimeout:       envconfig.Duration("STELLARMESH_LOGGING_WRITER_SHUTDOWN_TIMEOUT", 10*time.Second),
		HTTPTimeout:           envconfig.Duration("STELLARMESH_LOGGING_WRITER_HTTP_TIMEOUT", 5*time.Second),
		ObservabilityAddr:     envconfig.String("STELLARMESH_LOGGING_WRITER_OBSERVABILITY_ADDR", ":8092"),
	}
	if len(cfg.KafkaBrokers) == 0 || strings.TrimSpace(cfg.KafkaTopic) == "" {
		return Config{}, errors.New("Kafka brokers and topic are required")
	}
	if strings.TrimSpace(cfg.KafkaDLQTopic) == "" || cfg.KafkaDLQTopic == cfg.KafkaTopic {
		return Config{}, errors.New("Kafka DLQ topic is required and must differ from the source topic")
	}
	if strings.TrimSpace(cfg.ClickHouseDatabase) == "" {
		return Config{}, errors.New("STELLARMESH_LOGGING_CLICKHOUSE_DATABASE is required")
	}
	if strings.TrimSpace(cfg.ClickHouseUser) == "" {
		return Config{}, errors.New("STELLARMESH_LOGGING_CLICKHOUSE_USER is required")
	}
	if cfg.BatchSize <= 0 || cfg.BatchMaxBytes <= 0 || cfg.BatchMaxBytes > 1<<30 {
		return Config{}, errors.New("logging writer batch limits must be positive and bytes must not exceed 1 GiB")
	}
	if cfg.MaxSourceMessageBytes <= 0 || cfg.MaxSourceMessageBytes > 1<<30 {
		return Config{}, errors.New("STELLARMESH_LOGGING_WRITER_MAX_SOURCE_MESSAGE_BYTES must be between 1 byte and 1 GiB")
	}
	if strings.TrimSpace(cfg.ObservabilityAddr) == "" {
		return Config{}, errors.New("STELLARMESH_LOGGING_WRITER_OBSERVABILITY_ADDR is required")
	}
	return cfg, nil
}

func kafkaConnectionConfig() sharedkafka.ConnectionConfig {
	return sharedkafka.ConnectionConfig{
		ClientID:         "stellarmesh-logging-clickhouse-sink",
		SecurityProtocol: sharedkafka.SecurityProtocol(strings.ToUpper(envconfig.String("STELLARMESH_LOGGING_KAFKA_SECURITY_PROTOCOL", string(sharedkafka.SecurityProtocolPlaintext)))),
		SASLMechanism:    sharedkafka.SASLMechanism(strings.ToUpper(envconfig.String("STELLARMESH_LOGGING_KAFKA_SASL_MECHANISM", ""))),
		Username:         envconfig.String("STELLARMESH_LOGGING_KAFKA_USERNAME", ""),
		Password:         envconfig.String("STELLARMESH_LOGGING_KAFKA_PASSWORD", ""),
		TLSCAFile:        envconfig.String("STELLARMESH_LOGGING_KAFKA_TLS_CA_FILE", ""),
		TLSCertFile:      envconfig.String("STELLARMESH_LOGGING_KAFKA_TLS_CERT_FILE", ""),
		TLSKeyFile:       envconfig.String("STELLARMESH_LOGGING_KAFKA_TLS_KEY_FILE", ""),
		TLSServerName:    envconfig.String("STELLARMESH_LOGGING_KAFKA_TLS_SERVER_NAME", ""),
	}
}
