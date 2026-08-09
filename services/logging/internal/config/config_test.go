package config

import (
	"strings"
	"testing"

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
	if cfg.KafkaTopic != sharedlogging.TopicV1 || cfg.MaxRequestEvents != 512 {
		t.Fatalf("config = %#v", cfg)
	}
	if cfg.QueueCapacityEvents != 4096 || cfg.SpoolMaxBytes != 1<<30 || cfg.SpoolSegmentBytes != 16<<20 {
		t.Fatalf("buffer config = %#v", cfg)
	}
	if cfg.KafkaConnection.SecurityProtocol != "PLAINTEXT" {
		t.Fatalf("Kafka security protocol = %q", cfg.KafkaConnection.SecurityProtocol)
	}
}

func TestLoadReadsEventQueueAndSpoolByteSizes(t *testing.T) {
	t.Setenv("STELLARMESH_LOGGING_AUTH_FILE", "/run/secrets/logging-auth.json")
	t.Setenv("STELLARMESH_LOGGING_QUEUE_CAPACITY_EVENTS", "23")
	t.Setenv("STELLARMESH_LOGGING_SPOOL_MAX_BYTES", "2GiB")
	t.Setenv("STELLARMESH_LOGGING_SPOOL_SEGMENT_BYTES", "8MiB")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.QueueCapacityEvents != 23 || cfg.SpoolMaxBytes != 2<<30 || cfg.SpoolSegmentBytes != 8<<20 {
		t.Fatalf("config = %#v", cfg)
	}
}
