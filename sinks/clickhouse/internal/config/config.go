// Package config loads ClickHouse sink settings.
package config

import (
	"errors"
	"strings"
	"time"

	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/envconfig"
	sharedlogging "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging"
)

// Config contains Kafka consumer and ClickHouse writer settings.
type Config struct {
	KafkaBrokers       []string
	KafkaTopic         string
	KafkaGroupID       string
	ClickHouseHTTPURL  string
	ClickHouseDatabase string
	ClickHouseUser     string
	ClickHousePassword string
	BatchSize          int
	FlushInterval      time.Duration
	HTTPTimeout        time.Duration
}

// Load reads canonical STELLARMESH_LOGGING_* environment variables.
func Load() (Config, error) {
	cfg := Config{
		KafkaBrokers:       envconfig.CSV("STELLARMESH_LOGGING_KAFKA_BROKERS", "kafka:9092"),
		KafkaTopic:         envconfig.String("STELLARMESH_LOGGING_KAFKA_TOPIC", sharedlogging.TopicV1),
		KafkaGroupID:       envconfig.String("STELLARMESH_LOGGING_WRITER_GROUP_ID", "stellarmesh-logging-clickhouse"),
		ClickHouseHTTPURL:  envconfig.String("STELLARMESH_LOGGING_CLICKHOUSE_HTTP_URL", "http://clickhouse:8123"),
		ClickHouseDatabase: envconfig.String("STELLARMESH_LOGGING_CLICKHOUSE_DATABASE", ""),
		ClickHouseUser:     envconfig.String("STELLARMESH_LOGGING_CLICKHOUSE_USER", ""),
		ClickHousePassword: envconfig.String("STELLARMESH_LOGGING_CLICKHOUSE_PASSWORD", ""),
		BatchSize:          envconfig.Int("STELLARMESH_LOGGING_WRITER_BATCH_SIZE", 500),
		FlushInterval:      envconfig.Duration("STELLARMESH_LOGGING_WRITER_FLUSH_INTERVAL", time.Second),
		HTTPTimeout:        envconfig.Duration("STELLARMESH_LOGGING_WRITER_HTTP_TIMEOUT", 5*time.Second),
	}
	if len(cfg.KafkaBrokers) == 0 || strings.TrimSpace(cfg.KafkaTopic) == "" {
		return Config{}, errors.New("Kafka brokers and topic are required")
	}
	if strings.TrimSpace(cfg.ClickHouseDatabase) == "" {
		return Config{}, errors.New("STELLARMESH_LOGGING_CLICKHOUSE_DATABASE is required")
	}
	if strings.TrimSpace(cfg.ClickHouseUser) == "" {
		return Config{}, errors.New("STELLARMESH_LOGGING_CLICKHOUSE_USER is required")
	}
	return cfg, nil
}
