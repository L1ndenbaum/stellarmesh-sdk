// Package config loads logging ingester configuration.
package config

import (
	"errors"
	"strings"
	"time"

	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/envconfig"
	httpserver "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/http/server"
	sharedlogging "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging"
	sharedkafka "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/mq/kafka"
)

// Config contains the logging ingester runtime settings.
type Config struct {
	Addr                 string
	AuthFile             string
	DataDir              string
	ConsoleColor         bool
	ReadHeaderTimeout    time.Duration
	ReadTimeout          time.Duration
	WriteTimeout         time.Duration
	IdleTimeout          time.Duration
	ShutdownTimeout      time.Duration
	FlushInterval        time.Duration
	PublishTimeout       time.Duration
	ReplayInterval       time.Duration
	QueueCapacityEvents  int
	MaxBatchSize         int
	MaxRequestEvents     int
	KafkaBrokers         []string
	KafkaTopic           string
	KafkaConnection      sharedkafka.ConnectionConfig
	SpoolDir             string
	SpoolMaxBytes        int64
	SpoolSegmentBytes    int64
	SpoolReplayBatchSize int
}

// Load reads canonical STELLARMESH_LOGGING_* environment variables.
func Load() (Config, error) {
	dataDir := envconfig.String("STELLARMESH_LOGGING_DATA_DIR", "/var/lib/stellarmesh-logging")
	cfg := Config{
		Addr:                 envconfig.String("STELLARMESH_LOGGING_ADDR", ":8091"),
		AuthFile:             envconfig.String("STELLARMESH_LOGGING_AUTH_FILE", ""),
		DataDir:              dataDir,
		ConsoleColor:         envconfig.Bool("STELLARMESH_LOGGING_CONSOLE_COLOR", true),
		ReadHeaderTimeout:    envconfig.Duration("STELLARMESH_LOGGING_READ_HEADER_TIMEOUT", 5*time.Second),
		ReadTimeout:          envconfig.Duration("STELLARMESH_LOGGING_READ_TIMEOUT", 10*time.Second),
		WriteTimeout:         envconfig.Duration("STELLARMESH_LOGGING_WRITE_TIMEOUT", 10*time.Second),
		IdleTimeout:          envconfig.Duration("STELLARMESH_LOGGING_IDLE_TIMEOUT", 60*time.Second),
		ShutdownTimeout:      envconfig.Duration("STELLARMESH_LOGGING_SHUTDOWN_TIMEOUT", 10*time.Second),
		FlushInterval:        envconfig.Duration("STELLARMESH_LOGGING_BATCH_FLUSH_INTERVAL", 500*time.Millisecond),
		PublishTimeout:       envconfig.Duration("STELLARMESH_LOGGING_KAFKA_PUBLISH_TIMEOUT", 5*time.Second),
		ReplayInterval:       envconfig.Duration("STELLARMESH_LOGGING_KAFKA_REPLAY_INTERVAL", 5*time.Second),
		QueueCapacityEvents:  envconfig.Int("STELLARMESH_LOGGING_QUEUE_CAPACITY_EVENTS", 4096),
		MaxBatchSize:         envconfig.Int("STELLARMESH_LOGGING_MAX_BATCH_SIZE", 512),
		MaxRequestEvents:     envconfig.Int("STELLARMESH_LOGGING_MAX_REQUEST_EVENTS", 512),
		KafkaBrokers:         envconfig.CSV("STELLARMESH_LOGGING_KAFKA_BROKERS", "kafka:9092"),
		KafkaTopic:           envconfig.String("STELLARMESH_LOGGING_KAFKA_TOPIC", sharedlogging.TopicV1),
		KafkaConnection:      kafkaConnectionConfig("stellarmesh-logging-ingester"),
		SpoolDir:             envconfig.String("STELLARMESH_LOGGING_SPOOL_DIR", dataDir+"/spool"),
		SpoolMaxBytes:        envconfig.ByteSize("STELLARMESH_LOGGING_SPOOL_MAX_BYTES", 1<<30),
		SpoolSegmentBytes:    envconfig.ByteSize("STELLARMESH_LOGGING_SPOOL_SEGMENT_BYTES", 16<<20),
		SpoolReplayBatchSize: envconfig.Int("STELLARMESH_LOGGING_SPOOL_REPLAY_BATCH_SIZE", 128),
	}
	if strings.TrimSpace(cfg.AuthFile) == "" {
		return Config{}, errors.New("STELLARMESH_LOGGING_AUTH_FILE is required")
	}
	if len(cfg.KafkaBrokers) == 0 {
		return Config{}, errors.New("STELLARMESH_LOGGING_KAFKA_BROKERS is required")
	}
	if strings.TrimSpace(cfg.KafkaTopic) == "" {
		return Config{}, errors.New("STELLARMESH_LOGGING_KAFKA_TOPIC is required")
	}
	if cfg.QueueCapacityEvents <= 0 || cfg.MaxBatchSize <= 0 || cfg.MaxRequestEvents <= 0 {
		return Config{}, errors.New("logging queue and batch limits must be positive")
	}
	if cfg.PublishTimeout <= 0 {
		return Config{}, errors.New("STELLARMESH_LOGGING_KAFKA_PUBLISH_TIMEOUT must be positive")
	}
	if strings.TrimSpace(cfg.SpoolDir) == "" {
		return Config{}, errors.New("STELLARMESH_LOGGING_SPOOL_DIR is required")
	}
	return cfg, nil
}

func kafkaConnectionConfig(clientID string) sharedkafka.ConnectionConfig {
	return sharedkafka.ConnectionConfig{
		ClientID:         clientID,
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

// HTTPServerConfig returns bounded HTTP server settings.
func (cfg Config) HTTPServerConfig() httpserver.Config {
	return httpserver.Config{
		Addr: cfg.Addr, ReadHeaderTimeout: cfg.ReadHeaderTimeout, ReadTimeout: cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout, IdleTimeout: cfg.IdleTimeout,
	}
}
