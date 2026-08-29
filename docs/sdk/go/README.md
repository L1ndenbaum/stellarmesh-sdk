# Go 父 SDK 接入教程

本教程对应 Go Module `github.com/L1ndenbaum/stellarmesh-sdk/sdk/go`，适用于 Go 1.24 及以上版本。父 SDK `v0.3.0` 提供共享 HTTP、声明式 Gateway、对象存储、Storage 契约和环境变量解析；Logging 与 Kafka 已分别拆为独立轻量 Module。

## 安装固定版本

```sh
go get github.com/L1ndenbaum/stellarmesh-sdk/sdk/go@v0.3.0
go mod tidy
```

按需阅读对应教程：

- [Go Logging SDK](logging.md)：独立 `sdk/go/logging` Module，提供 Logging v1 契约、异步客户端和 `slog.Handler`；
- [Go Kafka SDK](kafka.md)：独立 `sdk/go/mq/kafka` Module，提供连接、认证、Publisher 和 Topic 检查；
- [Go 网关 SDK](gateway.md)：父 Module中的声明式 Gateway；
- [Go 对象存储 SDK](object-storage.md)：父 Module中的 S3/MinIO 进程内客户端。

父 SDK的 `gateway.WithAccessLogEmitter` 保持兼容，因此父 Module会传递依赖轻量 Logging Module；只使用 Logging 的项目应直接依赖 Logging Module，不需要安装父 SDK。

## 版本兼容

`sdk/go/v0.2.0` 仍不可变地包含旧 `sdk/go/logging` package，不能与新的 `sdk/go/logging/v0.1.0` 同时进入一个 build list，否则可能产生 `ambiguous import`。同时直接使用父 SDK和 Logging 的项目必须原子升级：

```sh
go get \
  github.com/L1ndenbaum/stellarmesh-sdk/sdk/go@v0.3.0 \
  github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging@v0.1.0
go mod tidy
```

如果还使用 Kafka，在同一次升级中固定：

```sh
go get \
  github.com/L1ndenbaum/stellarmesh-sdk/sdk/go@v0.3.0 \
  github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging@v0.1.0 \
  github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/mq/kafka@v0.1.0
go mod tidy
```

业务仓库不要提交长期本地 `replace`，也不要引用可变分支。升级前应阅读[发布与版本引用](../../release.md)，并在业务仓库执行自身的格式、静态检查和测试。
