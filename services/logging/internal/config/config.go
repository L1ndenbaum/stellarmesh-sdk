// Package config loads logging ingester configuration.
package config

import (
	"errors"
	"strings"
	"time"

	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/envconfig"
	httpserver "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/http/server"
	sharedlogging "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging"
)

// Config contains the logging ingester runtime settings.
type Config struct {
	Addr              string
	ServiceToken      string
	DataDir           string
	ConsoleColor      bool
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	ShutdownTimeout   time.Duration
	FlushInterval     time.Duration
	ReplayInterval    time.Duration
	QueueSize         int
	MaxBatchSize      int
	MaxRequestEvents  int
	KafkaBrokers      []string
	KafkaTopic        string
	SpoolFile         string
	ErrorAuditFile    string
}

// Load reads canonical STELLARMESH_LOGGING_* environment variables.
func Load() (Config, error) {
	dataDir := envconfig.String("STELLARMESH_LOGGING_DATA_DIR", "/var/lib/stellarmesh-logging")
	cfg := Config{
		Addr:              envconfig.String("STELLARMESH_LOGGING_ADDR", ":8091"),
		ServiceToken:      envconfig.String("STELLARMESH_LOGGING_TOKEN", ""),
		DataDir:           dataDir,
		ConsoleColor:      envconfig.Bool("STELLARMESH_LOGGING_CONSOLE_COLOR", true),
		ReadHeaderTimeout: envconfig.Duration("STELLARMESH_LOGGING_READ_HEADER_TIMEOUT", 5*time.Second),
		ReadTimeout:       envconfig.Duration("STELLARMESH_LOGGING_READ_TIMEOUT", 10*time.Second),
		WriteTimeout:      envconfig.Duration("STELLARMESH_LOGGING_WRITE_TIMEOUT", 10*time.Second),
		IdleTimeout:       envconfig.Duration("STELLARMESH_LOGGING_IDLE_TIMEOUT", 60*time.Second),
		ShutdownTimeout:   envconfig.Duration("STELLARMESH_LOGGING_SHUTDOWN_TIMEOUT", 10*time.Second),
		FlushInterval:     envconfig.Duration("STELLARMESH_LOGGING_BATCH_FLUSH_INTERVAL", 500*time.Millisecond),
		ReplayInterval:    envconfig.Duration("STELLARMESH_LOGGING_KAFKA_REPLAY_INTERVAL", 5*time.Second),
		QueueSize:         envconfig.Int("STELLARMESH_LOGGING_QUEUE_SIZE", 4096),
		MaxBatchSize:      envconfig.Int("STELLARMESH_LOGGING_MAX_BATCH_SIZE", 512),
		MaxRequestEvents:  envconfig.Int("STELLARMESH_LOGGING_MAX_REQUEST_EVENTS", 512),
		KafkaBrokers:      envconfig.CSV("STELLARMESH_LOGGING_KAFKA_BROKERS", "kafka:9092"),
		KafkaTopic:        envconfig.String("STELLARMESH_LOGGING_KAFKA_TOPIC", sharedlogging.TopicV1),
		SpoolFile:         envconfig.String("STELLARMESH_LOGGING_SPOOL_FILE", dataDir+"/spool/events.jsonl"),
		ErrorAuditFile:    envconfig.String("STELLARMESH_LOGGING_ERROR_AUDIT_FILE", dataDir+"/archive/error_audit.jsonl"),
	}
	if strings.TrimSpace(cfg.ServiceToken) == "" {
		return Config{}, errors.New("STELLARMESH_LOGGING_TOKEN is required")
	}
	if len(cfg.KafkaBrokers) == 0 {
		return Config{}, errors.New("STELLARMESH_LOGGING_KAFKA_BROKERS is required")
	}
	if strings.TrimSpace(cfg.KafkaTopic) == "" {
		return Config{}, errors.New("STELLARMESH_LOGGING_KAFKA_TOPIC is required")
	}
	return cfg, nil
}

// HTTPServerConfig returns bounded HTTP server settings.
func (cfg Config) HTTPServerConfig() httpserver.Config {
	return httpserver.Config{
		Addr: cfg.Addr, ReadHeaderTimeout: cfg.ReadHeaderTimeout, ReadTimeout: cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout, IdleTimeout: cfg.IdleTimeout,
	}
}
