package kafka_test

import (
	"context"
	"time"

	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/mq/kafka"
)

// ExampleNewPublisher 只编译验证；执行前需本地 Kafka 和已创建的 example-events Topic。
func ExampleNewPublisher() {
	publisher, err := kafka.NewPublisher(kafka.Config{Brokers: []string{"127.0.0.1:9092"}, Topic: "example-events"})
	if err != nil {
		panic(err)
	}
	defer func() {
		if err := publisher.Close(); err != nil {
			panic(err)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := publisher.Check(ctx); err != nil {
		panic(err)
	}
	if err := publisher.Publish(ctx, []kafka.Message{{Key: []byte("item-1"), Value: []byte(`{"name":"示例"}`)}}); err != nil {
		panic(err)
	}
}
