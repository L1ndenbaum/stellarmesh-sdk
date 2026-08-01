package kafka

import (
	"context"
	"strings"
	"testing"
)

func TestCheckRejectsMissingConfiguration(t *testing.T) {
	publisher := NewPublisher(Config{})
	err := publisher.Check(context.Background())
	if err == nil || !strings.Contains(err.Error(), "brokers") {
		t.Fatalf("error = %v", err)
	}
}
