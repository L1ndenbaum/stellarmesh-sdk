package config

import (
	"strings"
	"testing"
	"time"

	sharedlogging "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging"
)

func TestLoadRequiresAuthFile(t *testing.T) {
	t.Setenv("STELLARMESH_LOGGING_AUTH_FILE", "")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "AUTH_FILE") {
		t.Fatalf("error = %v", err)
	}
}

func TestLoadUsesCanonicalDefaults(t *testing.T) {
	t.Setenv("STELLARMESH_LOGGING_AUTH_FILE", "/run/secrets/logging-auth.json")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.KafkaTopic != sharedlogging.TopicV2 || cfg.MaxRequestEvents != 512 {
		t.Fatalf("config = %#v", cfg)
	}
	if cfg.QueueCapacityEvents != 4096 || cfg.QueueCapacityBytes != 16<<20 || cfg.MaxBatchBytes != 4<<20 ||
		cfg.SpoolMaxBytes != 1<<30 || cfg.SpoolSegmentBytes != 16<<20 || cfg.PublishTimeout != 5*time.Second {
		t.Fatalf("buffer config = %#v", cfg)
	}
	if cfg.KafkaConnection.SecurityProtocol != "PLAINTEXT" {
		t.Fatalf("Kafka security protocol = %q", cfg.KafkaConnection.SecurityProtocol)
	}
}

func TestLoadReadsPublishTimeout(t *testing.T) {
	t.Setenv("STELLARMESH_LOGGING_AUTH_FILE", "/run/secrets/logging-auth.json")
	t.Setenv("STELLARMESH_LOGGING_KAFKA_PUBLISH_TIMEOUT", "3s")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PublishTimeout != 3*time.Second {
		t.Fatalf("publish timeout = %s", cfg.PublishTimeout)
	}
}

func TestLoadIgnoresRemovedConsoleColorSetting(t *testing.T) {
	t.Setenv("STELLARMESH_LOGGING_AUTH_FILE", "/run/secrets/logging-auth.json")
	t.Setenv("STELLARMESH_LOGGING_CONSOLE_COLOR", "legacy-value")
	if _, err := Load(); err != nil {
		t.Fatalf("Load() rejected removed console color setting: %v", err)
	}
}

func TestLoadReadsEventQueueAndSpoolByteSizes(t *testing.T) {
	t.Setenv("STELLARMESH_LOGGING_AUTH_FILE", "/run/secrets/logging-auth.json")
	t.Setenv("STELLARMESH_LOGGING_QUEUE_CAPACITY_EVENTS", "23")
	t.Setenv("STELLARMESH_LOGGING_QUEUE_CAPACITY_BYTES", "24MiB")
	t.Setenv("STELLARMESH_LOGGING_MAX_BATCH_BYTES", "3MiB")
	t.Setenv("STELLARMESH_LOGGING_SPOOL_MAX_BYTES", "2GiB")
	t.Setenv("STELLARMESH_LOGGING_SPOOL_SEGMENT_BYTES", "8MiB")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.QueueCapacityEvents != 23 || cfg.QueueCapacityBytes != 24<<20 || cfg.MaxBatchBytes != 3<<20 ||
		cfg.SpoolMaxBytes != 2<<30 || cfg.SpoolSegmentBytes != 8<<20 {
		t.Fatalf("config = %#v", cfg)
	}
}

func TestLoadRejectsInvalidAndOutOfBoundsValues(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{name: "invalid duration", key: "STELLARMESH_LOGGING_KAFKA_PUBLISH_TIMEOUT", value: "forever"},
		{name: "queue event upper bound", key: "STELLARMESH_LOGGING_QUEUE_CAPACITY_EVENTS", value: "1000001"},
		{name: "queue byte upper bound", key: "STELLARMESH_LOGGING_QUEUE_CAPACITY_BYTES", value: "2GiB"},
		{name: "spool byte upper bound", key: "STELLARMESH_LOGGING_SPOOL_MAX_BYTES", value: "2TiB"},
		{name: "spool byte lower bound", key: "STELLARMESH_LOGGING_SPOOL_MAX_BYTES", value: "1MiB"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("STELLARMESH_LOGGING_AUTH_FILE", "/run/secrets/logging-auth.json")
			t.Setenv(test.key, test.value)
			if _, err := Load(); err == nil {
				t.Fatal("Load() accepted invalid configuration")
			}
		})
	}
}
