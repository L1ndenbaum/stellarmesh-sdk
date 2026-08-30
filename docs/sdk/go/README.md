# Go 父 SDK 接入教程

本教程对应 Go Module `github.com/L1ndenbaum/stellarmesh-sdk/sdk/go`，适用于 Go 1.24 及以上版本。当前公开父 SDK `v0.4.0` 提供共享 HTTP、对象存储和环境变量解析；Gateway、Logging 与 Kafka 已分别拆为独立 Module。

`v0.4.0` 的不可变源码中仍包含旧 `storagecontract` package，但它把 `storage-service` 的 HTTP DTO 和访问策略暴露成了通用 SDK。当前本地 `dev` 源码已移除该 package：语言无关的 Storage v1 定义继续由 `contracts/storage/v1` 管理，Go 实现归入 `services/storage/internal/storagev1`。业务项目不应新增对旧 package 的依赖；这一删除尚未发布，后续父 SDK 发版时必须按破坏性 Module 边界变化处理。

## 安装固定版本

```sh
go get github.com/L1ndenbaum/stellarmesh-sdk/sdk/go@v0.4.0
go mod tidy
```

按需阅读对应教程：

- [Go 网关 SDK](gateway.md)：独立 `sdk/go/gateway` Module，提供声明式 Gateway、JWT 认证和 Redis 限流；本地 `dev` 源码还包含尚未发布的独立 Logging Adapter；
- [Go Logging SDK](logging.md)：独立 `sdk/go/logging` Module，提供 Logging v1 契约、异步客户端和 `slog.Handler`；
- [Go Kafka SDK](kafka.md)：独立 `sdk/go/mq/kafka` Module，提供连接、认证、Publisher 和 Topic 检查；
- [Go 对象存储 SDK](object-storage.md)：父 Module中的 S3/MinIO 进程内客户端。

## 版本兼容

`sdk/go/v0.3.0` 仍不可变地包含旧 `sdk/go/gateway` package，不能与独立 Gateway Module 同时进入一个 build list。`sdk/go/v0.2.0` 仍包含旧 Logging package，`sdk/go/v0.1.1` 仍包含旧 Kafka package。错误组合可能产生 `ambiguous import`，不能通过长期 `replace` 绕过。`storagecontract` 的内部化只影响当前未发布源码，不改变这些历史 tag。

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
