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
}
