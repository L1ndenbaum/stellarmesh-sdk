# Go Kafka

适用于 Go 1.24；需要外部 Kafka 及业务管理的 Topic、ACL 和凭据。

## 安装

示例适用于 `v0.1.0`；其他组件各自独立版本，参阅[发布矩阵](https://github.com/L1ndenbaum/stellarmesh-sdk/blob/dev/docs/release.md#当前制品矩阵)。

```sh
go get github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/mq/kafka@v0.1.0
```

## 最小完整示例

本例只编译验证，不自动连接 Kafka。执行前需本地 broker 和 example-events Topic；应用复用 Publisher 并负责 Close，写失败不代表消息未投递。 将源码作为外部包的 `example_test.go`，使用 `go test` 编译；只有带 `Output` 的示例会被执行。

<!-- example: sdk/go/mq/kafka/example_test.go -->
```go
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
```
<!-- /example -->

## 关键限制与深入指南

详细配置、错误与迁移见[接入指南](https://github.com/L1ndenbaum/stellarmesh-sdk/blob/dev/docs/sdk/go/kafka.md)。同时引入父 Module 与嵌套 Module 时，父 Module 需采用拆分完成后的版本，不能用长期本地 replace 掩盖 ambiguous import。源码布局与验证见[贡献指南](https://github.com/L1ndenbaum/stellarmesh-sdk/blob/dev/CONTRIBUTING.md)。
