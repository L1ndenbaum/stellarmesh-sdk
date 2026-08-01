package config

import (
	"strings"
	"testing"

	sharedlogging "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging"
)

func TestLoadRequiresToken(t *testing.T) {
	t.Setenv("STELLARMESH_LOGGING_TOKEN", "")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "TOKEN") {
		t.Fatalf("error = %v", err)
	}
}

func TestLoadUsesCanonicalDefaults(t *testing.T) {
	t.Setenv("STELLARMESH_LOGGING_TOKEN", "token")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.KafkaTopic != sharedlogging.TopicV1 || cfg.MaxRequestEvents != 512 {
		t.Fatalf("config = %#v", cfg)
	}
}
