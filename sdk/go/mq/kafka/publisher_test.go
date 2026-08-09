package kafka

import (
	"context"
	"strings"
	"testing"
)

func TestCheckRejectsMissingConfiguration(t *testing.T) {
	publisher, createErr := NewPublisher(Config{})
	if createErr != nil {
		t.Fatal(createErr)
	}
	err := publisher.Check(context.Background())
	if err == nil || !strings.Contains(err.Error(), "brokers") {
		t.Fatalf("error = %v", err)
	}
}
