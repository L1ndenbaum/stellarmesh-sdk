// Package config 加载日志接收服务配置。
package config

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/envconfig"
	httpserver "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/http/server"
	sharedlogging "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging"
	sharedkafka "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/mq/kafka"
)

const (
	maxQueueCapacityEvents = 1_000_000
	maxQueueCapacityBytes  = int64(1 << 30)
	maxBatchEvents         = 10_000
	maxBatchBytes          = int64(64 << 20)
	maxSpoolBytes          = int64(1 << 40)
	minimumSpoolBytes      = int64(2*(sharedlogging.MaxEventJSONBytesV2+1) + (64 << 10))
	maxRuntimeDuration     = 24 * time.Hour
)

// Config 包含日志接收服务运行时设置。
type Config struct {
	Addr                 string
	AuthFile             string
	DataDir              string
	ReadHeaderTimeout    time.Duration
	ReadTimeout          time.Duration
	WriteTimeout         time.Duration
	IdleTimeout          time.Duration
	ShutdownTimeout      time.Duration
	FlushInterval        time.Duration
	PublishTimeout       time.Duration
	ReplayInterval       time.Duration
	QueueCapacityEvents  int
	QueueCapacityBytes   int64
	MaxBatchSize         int
	MaxBatchBytes        int64
	MaxRequestEvents     int
	KafkaBrokers         []string
	KafkaTopic           string
	KafkaConnection      sharedkafka.ConnectionConfig
	SpoolDir             string
	SpoolMaxBytes        int64
	SpoolSegmentBytes    int64
	SpoolReplayBatchSize int
}

// Load 读取规范的 STELLARMESH_LOGGING_* 环境变量。
func Load() (Config, error) {
	loader := envconfig.NewStrictLoader()
	dataDir := envconfig.String("STELLARMESH_LOGGING_DATA_DIR", "/var/lib/stellarmesh-logging")
	cfg := Config{
		Addr:                 envconfig.String("STELLARMESH_LOGGING_ADDR", ":8091"),
		AuthFile:             envconfig.String("STELLARMESH_LOGGING_AUTH_FILE", ""),
		DataDir:              dataDir,
		ReadHeaderTimeout:    loader.Duration("STELLARMESH_LOGGING_READ_HEADER_TIMEOUT", 5*time.Second),
		ReadTimeout:          loader.Duration("STELLARMESH_LOGGING_READ_TIMEOUT", 10*time.Second),
		WriteTimeout:         loader.Duration("STELLARMESH_LOGGING_WRITE_TIMEOUT", 10*time.Second),
		IdleTimeout:          loader.Duration("STELLARMESH_LOGGING_IDLE_TIMEOUT", 60*time.Second),
		ShutdownTimeout:      loader.Duration("STELLARMESH_LOGGING_SHUTDOWN_TIMEOUT", 10*time.Second),
		FlushInterval:        loader.Duration("STELLARMESH_LOGGING_BATCH_FLUSH_INTERVAL", 500*time.Millisecond),
		PublishTimeout:       loader.Duration("STELLARMESH_LOGGING_KAFKA_PUBLISH_TIMEOUT", 5*time.Second),
		ReplayInterval:       loader.Duration("STELLARMESH_LOGGING_KAFKA_REPLAY_INTERVAL", 5*time.Second),
		QueueCapacityEvents:  loader.Int("STELLARMESH_LOGGING_QUEUE_CAPACITY_EVENTS", 4096),
		QueueCapacityBytes:   loader.ByteSize("STELLARMESH_LOGGING_QUEUE_CAPACITY_BYTES", 16<<20),
		MaxBatchSize:         loader.Int("STELLARMESH_LOGGING_MAX_BATCH_SIZE", 512),
		MaxBatchBytes:        loader.ByteSize("STELLARMESH_LOGGING_MAX_BATCH_BYTES", 4<<20),
		MaxRequestEvents:     loader.Int("STELLARMESH_LOGGING_MAX_REQUEST_EVENTS", 512),
		KafkaBrokers:         loader.CSV("STELLARMESH_LOGGING_KAFKA_BROKERS", "kafka:9092"),
		KafkaTopic:           envconfig.String("STELLARMESH_LOGGING_KAFKA_TOPIC", sharedlogging.TopicV2),
		KafkaConnection:      kafkaConnectionConfig("stellarmesh-logging-ingester"),
		SpoolDir:             envconfig.String("STELLARMESH_LOGGING_SPOOL_DIR", dataDir+"/spool"),
		SpoolMaxBytes:        loader.ByteSize("STELLARMESH_LOGGING_SPOOL_MAX_BYTES", 1<<30),
		SpoolSegmentBytes:    loader.ByteSize("STELLARMESH_LOGGING_SPOOL_SEGMENT_BYTES", 16<<20),
		SpoolReplayBatchSize: loader.Int("STELLARMESH_LOGGING_SPOOL_REPLAY_BATCH_SIZE", 128),
	}
	if err := loader.Err(); err != nil {
		return Config{}, err
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
	if cfg.QueueCapacityEvents <= 0 || cfg.QueueCapacityEvents > maxQueueCapacityEvents ||
		cfg.QueueCapacityBytes < sharedlogging.MaxEventJSONBytesV2 || cfg.QueueCapacityBytes > maxQueueCapacityBytes {
		return Config{}, errors.New("logging queue limits are outside supported bounds")
	}
	if cfg.MaxBatchSize <= 0 || cfg.MaxBatchSize > maxBatchEvents ||
		cfg.MaxBatchBytes <= 0 || cfg.MaxBatchBytes > maxBatchBytes ||
		cfg.MaxRequestEvents <= 0 || cfg.MaxRequestEvents > maxBatchEvents {
		return Config{}, errors.New("logging batch and request limits are outside supported bounds")
	}
	for _, setting := range []struct {
		name  string
		value time.Duration
	}{
		{name: "read header timeout", value: cfg.ReadHeaderTimeout},
		{name: "read timeout", value: cfg.ReadTimeout},
		{name: "write timeout", value: cfg.WriteTimeout},
		{name: "idle timeout", value: cfg.IdleTimeout},
		{name: "shutdown timeout", value: cfg.ShutdownTimeout},
		{name: "flush interval", value: cfg.FlushInterval},
		{name: "publish timeout", value: cfg.PublishTimeout},
		{name: "replay interval", value: cfg.ReplayInterval},
	} {
		if setting.value <= 0 || setting.value > maxRuntimeDuration {
			return Config{}, fmt.Errorf("logging %s is outside supported bounds", setting.name)
		}
	}
	if strings.TrimSpace(cfg.SpoolDir) == "" {
		return Config{}, errors.New("STELLARMESH_LOGGING_SPOOL_DIR is required")
	}
	if cfg.SpoolMaxBytes < minimumSpoolBytes || cfg.SpoolMaxBytes > maxSpoolBytes ||
		cfg.SpoolSegmentBytes <= 0 || cfg.SpoolSegmentBytes > maxBatchBytes || cfg.SpoolSegmentBytes > cfg.SpoolMaxBytes ||
		cfg.SpoolReplayBatchSize <= 0 || cfg.SpoolReplayBatchSize > maxBatchEvents {
		return Config{}, errors.New("logging spool limits are outside supported bounds")
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

// HTTPServerConfig 返回设置了边界的 HTTP 服务配置。
func (cfg Config) HTTPServerConfig() httpserver.Config {
	return httpserver.Config{
		Addr: cfg.Addr, ReadHeaderTimeout: cfg.ReadHeaderTimeout, ReadTimeout: cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout, IdleTimeout: cfg.IdleTimeout,
	}
}
