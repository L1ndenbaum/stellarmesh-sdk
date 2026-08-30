package config

import (
	"strings"
	"testing"

	sharedlogging "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging"
)

func TestLoadRequiresDatabaseAndUser(t *testing.T) {
	t.Setenv("STELLARMESH_LOGGING_CLICKHOUSE_DATABASE", "")
	t.Setenv("STELLARMESH_LOGGING_CLICKHOUSE_USER", "")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "DATABASE") {
		t.Fatalf("error = %v", err)
	}
}

func TestLoadUsesCanonicalTopic(t *testing.T) {
	t.Setenv("STELLARMESH_LOGGING_CLICKHOUSE_DATABASE", "logging_db")
	t.Setenv("STELLARMESH_LOGGING_CLICKHOUSE_USER", "logging_runtime")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.KafkaTopic != sharedlogging.TopicV1 {
		t.Fatalf("topic = %q", cfg.KafkaTopic)
	}
	if cfg.KafkaDLQTopic != sharedlogging.DeadLetterTopicV1 || cfg.ObservabilityAddr != ":8092" {
		t.Fatalf("runtime config = %#v", cfg)
	}
	if cfg.MaxSourceMessageBytes != 1<<20 {
		t.Fatalf("max source message bytes = %d", cfg.MaxSourceMessageBytes)
	}
	if cfg.BatchMaxBytes != 16<<20 {
		t.Fatalf("batch max bytes = %d", cfg.BatchMaxBytes)
	}
	if cfg.KafkaConnection.SecurityProtocol != "PLAINTEXT" {
		t.Fatalf("Kafka security protocol = %q", cfg.KafkaConnection.SecurityProtocol)
	}
}

func TestLoadReadsWriterBatchByteLimit(t *testing.T) {
	t.Setenv("STELLARMESH_LOGGING_CLICKHOUSE_DATABASE", "logging_db")
	t.Setenv("STELLARMESH_LOGGING_CLICKHOUSE_USER", "logging_runtime")
	t.Setenv("STELLARMESH_LOGGING_WRITER_BATCH_MAX_BYTES", "8MiB")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BatchMaxBytes != 8<<20 {
		t.Fatalf("batch max bytes = %d", cfg.BatchMaxBytes)
	}
}

func TestLoadRejectsSourceTopicAsDLQ(t *testing.T) {
	t.Setenv("STELLARMESH_LOGGING_CLICKHOUSE_DATABASE", "logging_db")
	t.Setenv("STELLARMESH_LOGGING_CLICKHOUSE_USER", "logging_runtime")
	t.Setenv("STELLARMESH_LOGGING_KAFKA_TOPIC", "same-topic")
	t.Setenv("STELLARMESH_LOGGING_KAFKA_DLQ_TOPIC", "same-topic")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "DLQ") {
		t.Fatalf("error = %v", err)
	}
}

func TestLoadRejectsUnsafeSourceMessageLimits(t *testing.T) {
	tests := []struct {
		name        string
		sourceBytes string
		batchBytes  string
	}{
		{name: "source exceeds contract", sourceBytes: "2MiB", batchBytes: "16MiB"},
		{name: "batch cannot hold source", sourceBytes: "1MiB", batchBytes: "512KiB"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("STELLARMESH_LOGGING_CLICKHOUSE_DATABASE", "logging_db")
			t.Setenv("STELLARMESH_LOGGING_CLICKHOUSE_USER", "logging_runtime")
			t.Setenv("STELLARMESH_LOGGING_WRITER_MAX_SOURCE_MESSAGE_BYTES", test.sourceBytes)
			t.Setenv("STELLARMESH_LOGGING_WRITER_BATCH_MAX_BYTES", test.batchBytes)
			if _, err := Load(); err == nil {
				t.Fatal("Load() accepted unsafe source message limits")
			}
		})
	}
}

func TestLoadRejectsInvalidAndOutOfBoundsWriterValues(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{name: "invalid duration", key: "STELLARMESH_LOGGING_WRITER_HTTP_TIMEOUT", value: "forever"},
		{name: "invalid integer", key: "STELLARMESH_LOGGING_WRITER_BATCH_SIZE", value: "many"},
		{name: "batch count upper bound", key: "STELLARMESH_LOGGING_WRITER_BATCH_SIZE", value: "10001"},
		{name: "batch byte upper bound", key: "STELLARMESH_LOGGING_WRITER_BATCH_MAX_BYTES", value: "65MiB"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("STELLARMESH_LOGGING_CLICKHOUSE_DATABASE", "logging_db")
			t.Setenv("STELLARMESH_LOGGING_CLICKHOUSE_USER", "logging_runtime")
			t.Setenv(test.key, test.value)
			if _, err := Load(); err == nil {
				t.Fatal("Load() accepted invalid configuration")
			}
		})
	}
}
