# Go 父 SDK 接入教程

本教程对应 Go Module `github.com/L1ndenbaum/stellarmesh-sdk/sdk/go`，适用于 Go 1.24 及以上版本。父 SDK `v0.4.0` 提供共享 HTTP、对象存储、Storage 契约和环境变量解析；Gateway、Logging 与 Kafka 已分别拆为独立 Module。

## 安装固定版本

```sh
go get github.com/L1ndenbaum/stellarmesh-sdk/sdk/go@v0.4.0
go mod tidy
```

按需阅读对应教程：

- [Go 网关 SDK](gateway.md)：独立 `sdk/go/gateway` Module，提供声明式 Gateway、JWT 认证和 Redis 限流；
- [Go Logging SDK](logging.md)：独立 `sdk/go/logging` Module，提供 Logging v1 契约、异步客户端和 `slog.Handler`；
- [Go Kafka SDK](kafka.md)：独立 `sdk/go/mq/kafka` Module，提供连接、认证、Publisher 和 Topic 检查；
- [Go 对象存储 SDK](object-storage.md)：父 Module中的 S3/MinIO 进程内客户端。

## 版本兼容

`sdk/go/v0.3.0` 仍不可变地包含旧 `sdk/go/gateway` package，不能与独立 Gateway Module 同时进入一个 build list。`sdk/go/v0.2.0` 仍包含旧 Logging package，`sdk/go/v0.1.1` 仍包含旧 Kafka package。错误组合可能产生 `ambiguous import`，不能通过长期 `replace` 绕过。

同时使用父 SDK 与 Gateway 的项目必须原子升级：

```sh
go get \
  github.com/L1ndenbaum/stellarmesh-sdk/sdk/go@v0.4.0 \
  github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/gateway@v0.2.0
go mod tidy
```

如果还直接使用 Logging 和 Kafka，在同一次升级中固定全部版本：

```sh
go get \
  github.com/L1ndenbaum/stellarmesh-sdk/sdk/go@v0.4.0 \
  github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/gateway@v0.2.0 \
  github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging@v0.1.0 \
  github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/mq/kafka@v0.1.0
go mod tidy
```

业务仓库不要引用可变分支。升级前应阅读[发布与版本引用](../../release.md)，并在业务仓库执行自身的格式、静态检查和测试。
