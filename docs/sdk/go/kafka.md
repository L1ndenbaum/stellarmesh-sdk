# Go Kafka SDK 接入教程

Kafka SDK 是独立 Go module：

```text
github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/mq/kafka
```

它只依赖 `kafka-go` 及其压缩、SCRAM 传递依赖，不会因为项目只需要 Kafka 而引入父 SDK 中的 AWS SDK、Gateway、JWT 或 Redis。当前公开版本为 `v0.1.0`，要求 Go 1.24 及以上版本。

## 1. 安装与升级

只使用 Kafka 能力的项目直接安装独立 Module：

```sh
go get github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/mq/kafka@v0.1.0
go mod tidy
```

如果项目同时使用 logging、gateway、objectstorage 等父 SDK package，必须原子升级两个 Module：

```sh
go get \
  github.com/L1ndenbaum/stellarmesh-sdk/sdk/go@v0.2.0 \
  github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/mq/kafka@v0.1.0
go mod tidy
```

`sdk/go/v0.1.1` 仍包含旧的 `mq/kafka` package。它不能与新的 Kafka Module 同时使用，否则同一个 import path 会同时由两个 Module 提供，Go 可能报告 `ambiguous import`。已有 tag 保持不可变；正确迁移方式是把父 SDK 升级到已经移除 Kafka package 的 `v0.2.0`，而不是为任一 Module 添加长期 `replace`。

## 2. 能力和责任边界

本 Module 提供：

- `Connection`：统一构造 `kafka-go` 的 `Dialer` 与 `Transport`；
- `Publisher`：使用 Hash 分区、`RequireAll` 确认和调用方 context 发布消息；
- `CheckTopic`：并行探测去重后的 broker，确认既有 Topic 至少有一个 partition；
- `IsMessageTooLarge`：识别直接或嵌套在 `WriteErrors` 中的消息过大错误；
- `PLAIN`、`SCRAM-SHA-256`、`SCRAM-SHA-512` 三种 SASL 机制；
- `PLAINTEXT`、`TLS`、`SASL_PLAINTEXT`、`SASL_TLS` 四种传输模式，以及自定义 CA、mTLS 和 TLS server name。

本 Module 不创建 Topic 或 ACL，不管理 consumer group，不替业务决定 offset 提交与重试语义，也不提供 Schema Registry、事务生产者、OAuth、GSSAPI 或无限重试。Topic、principal、ACL、证书和 Secret 仍由业务部署或 `server-infrastructure` 准备。

## 3. 安全配置

业务项目应在自己的配置层读取环境变量或 Secret，再显式构造 `ConnectionConfig`。SDK 不读取业务 settings 或部署目录。

| `SecurityProtocol` | 加密 | SASL | 典型用途 |
| --- | --- | --- | --- |
| 空值或 `PLAINTEXT` | 否 | 否 | 受控开发网络 |
| `TLS` | 是 | 否 | TLS 或 mTLS 身份 |
| `SASL_PLAINTEXT` | 否 | 是 | 受控开发网络中的 SASL 联调 |
| `SASL_TLS` | 是 | 是 | 推荐的生产 SASL 模式 |

使用 SASL 时必须显式选择 `PLAIN`、`SCRAM-SHA-256` 或 `SCRAM-SHA-512`，并提供非空用户名和密码。未知机制会在构造阶段失败。生产环境通常使用 `SASL_TLS`；`SASL_PLAINTEXT` 会让凭据依赖网络边界保护，不适合不可信网络。

```go
connection, err := stellarkafka.NewConnection(stellarkafka.ConnectionConfig{
    ClientID:         "orders-worker",
    SecurityProtocol: stellarkafka.SecurityProtocolSASLTLS,
    SASLMechanism:    stellarkafka.SASLMechanismSCRAMSHA512,
    Username:         config.KafkaUsername,
    Password:         config.KafkaPassword,
    TLSCAFile:        config.KafkaCAFile,
    TLSServerName:    "kafka.internal",
    DialTimeout:      10 * time.Second,
})
if err != nil {
    return fmt.Errorf("创建 Kafka 连接配置: %w", err)
}
```

TLS 最低版本固定为 TLS 1.2。客户端证书和私钥必须同时配置：

```go
connection, err := stellarkafka.NewConnection(stellarkafka.ConnectionConfig{
    SecurityProtocol: stellarkafka.SecurityProtocolTLS,
    TLSCAFile:        config.KafkaCAFile,
    TLSCertFile:      config.KafkaClientCertFile,
    TLSKeyFile:       config.KafkaClientKeyFile,
    TLSServerName:    "kafka.internal",
})
```

SDK 不把密码写入错误文本，但业务项目仍不得记录完整 `ConnectionConfig`、SASL 密码、私钥内容或 Secret 来源。

## 4. 构造 Consumer

SDK 有意不封装 Reader。业务项目通过 `Connection.Dialer()` 创建自己的 `kafka-go.Reader`，从而显式拥有 consumer group、offset、提交和重试策略：

```go
import (
    segmentio "github.com/segmentio/kafka-go"
    stellarkafka "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/mq/kafka"
)

connection, err := stellarkafka.NewConnection(connectionConfig)
if err != nil {
    return err
}

reader := segmentio.NewReader(segmentio.ReaderConfig{
    Brokers:  config.Brokers,
    Topic:    config.Topic,
    GroupID:  config.ConsumerGroup,
    Dialer:   connection.Dialer(),
    MinBytes: 1,
    MaxBytes: 1 << 20,
})
defer reader.Close()
```

使用 consumer group 时，是否调用 `ReadMessage` 自动提交，还是使用 `FetchMessage` 加 `CommitMessages` 显式提交，是业务可靠性语义的一部分。SDK 不隐藏该选择，也不会在 handler 成功以前代替业务提交 offset。

## 5. 构造 Publisher

一个进程通常共享一个 `Publisher`。先用有界 context 检查既有 Topic，再开始接收依赖 Kafka 的业务流量：

```go
publisher, err := stellarkafka.NewPublisher(stellarkafka.Config{
    Brokers:      config.Brokers,
    Topic:        config.Topic,
    BatchTimeout: 100 * time.Millisecond,
    BatchBytes:   1 << 20,
    Connection:   connectionConfig,
})
if err != nil {
    return err
}
defer publisher.Close()

checkCtx, cancelCheck := context.WithTimeout(context.Background(), 10*time.Second)
defer cancelCheck()
if err := publisher.Check(checkCtx); err != nil {
    return fmt.Errorf("检查 Kafka Topic: %w", err)
}

publishCtx, cancelPublish := context.WithTimeout(context.Background(), 5*time.Second)
defer cancelPublish()
if err := publisher.Publish(publishCtx, []stellarkafka.Message{{
    Key:   []byte(orderID),
    Value: payload,
    Time:  time.Now().UTC(),
}}); err != nil {
    return fmt.Errorf("发布 Kafka 消息: %w", err)
}
```

`Publisher` 使用 `kafka-go.Hash` 按 Key 稳定分区，并要求 `RequireAll`。调用方必须给 `Check` 和 `Publish` 提供符合业务预算的 context；SDK 不增加自定义无限重试。关闭顺序应先停止产生新消息，再等待业务中的在途发布完成，最后调用 `Close`。

`BatchBytes` 小于零会在构造阶段失败；填零时沿用 `kafka-go` 默认批量字节行为。消息格式、单条大小和批量大小仍必须与业务协议以及 broker 的 `max.message.bytes` 保持一致。

## 6. Topic 检查和错误分类

已有 `Connection` 时，可以直接检查任意既有 Topic：

```go
err := stellarkafka.CheckTopic(ctx, connection.Dialer(), config.Brokers, config.Topic)
```

检查会去除空 broker、对重复地址去重并并行探测。任一 broker 能访问目标 Topic 且返回至少一个 partition 即成功，并取消其他探测；全部失败时返回聚合错误。调用方 context 超时或取消时返回对应 context 错误。检查只读元数据，绝不创建 Topic。

发布失败后可以判断是否属于消息大小问题：

```go
if stellarkafka.IsMessageTooLarge(err) {
    // 按业务协议拒绝、隔离或转入受控 DLQ，不要无限重试。
}
```

其他错误的重试、降级、readiness 和告警策略由业务服务决定。鉴权失败、配置错误和确定的消息过大不应当作为临时网络错误无限重试。

## 7. 接入验收

业务仓库接入后至少验证：

1. `go list -m all` 中不存在仅因 Kafka 引入的 AWS SDK、Chi、JWT 或 Redis；
2. 开发环境的 `PLAINTEXT` 或 SASL 联调能够连接已存在的 Topic；
3. 生产候选配置使用预期的 SASL 机制、CA、server name，并在需要时加载 mTLS 证书；
4. 错误用户名、密码、ACL 或 Topic 能让 readiness fail-close，而不是退化为匿名访问；
5. Consumer 只在业务处理达到约定持久点后提交 offset；
6. Publisher 超时、消息过大和关闭路径均有明确指标或日志；
7. 如果项目还依赖父 SDK，`go.mod` 同时固定父 `v0.2.0` 和 Kafka `v0.1.0`，且没有 `ambiguous import`。
