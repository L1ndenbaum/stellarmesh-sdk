package infrastructure

import (
	"context"
	"testing"

	"github.com/L1ndenbaum/stellarmesh-sdk/sinks/logging/clickhouse/internal/application"
)

func TestKafkaSourceRejectsUnknownCommitHandle(t *testing.T) {
	source := &KafkaSource{}
	err := source.Commit(context.Background(), []application.Message{{Handle: "not-a-kafka-message"}})
	if err == nil {
		t.Fatal("Commit() accepted an unknown offset handle")
	}
}
