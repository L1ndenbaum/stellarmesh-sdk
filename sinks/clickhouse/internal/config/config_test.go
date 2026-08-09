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
	if cfg.KafkaConnection.SecurityProtocol != "PLAINTEXT" {
		t.Fatalf("Kafka security protocol = %q", cfg.KafkaConnection.SecurityProtocol)
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
