# Go 父 SDK 接入教程

本教程对应 Go Module `github.com/L1ndenbaum/stellarmesh-sdk/sdk/go`，适用于 Go 1.24 及以上版本。`v0.5.0` 是标准库基础 Module，只提供环境配置、严格 JSON 请求解码和 HTTP server 构造，不再聚合 Gateway、Logging、Kafka 或 Object Storage 的第三方依赖。

## 安装固定版本

```sh
go get github.com/L1ndenbaum/stellarmesh-sdk/sdk/go@v0.5.0
go mod tidy
```

父 Module 包含：

- `envconfig`：基础环境变量解析与显式失败的严格 loader；
- `http/jsonbody`：有大小上限、可拒绝未知字段且不写业务响应的 JSON 解码；
- `http/server`：统一 HTTP timeout 与优雅关闭所需的 server 构造。

父 Module 不再提供 `ApiEnvelope`、token 认证、通用 Router、可信客户端 IP 解析或对象存储。具体服务应拥有自己的响应协议与认证规则；业务 Gateway 应使用独立 Gateway Module；S3/MinIO 接入应使用独立 Object Storage Module。

按需阅读对应教程：

- [Go 网关 SDK](gateway.md)：`sdk/go/gateway@v0.3.0`，提供声明式 Gateway、JWT 认证、Redis 限流与标准库访问日志；
- [Go Logging SDK](logging.md)：`sdk/go/logging@v0.3.0`，提供零第三方依赖的 `slog.Handler` 安全装饰器；
- [Go Kafka SDK](kafka.md)：`sdk/go/mq/kafka@v0.1.0`，提供连接、认证、Publisher 和 Topic 检查；
- [Go 对象存储 SDK](object-storage.md)：`sdk/go/objectstorage@v0.1.0`，提供 S3/MinIO 进程内客户端。

## 版本兼容

历史父 SDK 包含后来拆出的相同 import path，错误组合会产生 `ambiguous import`：

- `sdk/go/v0.1.1` 包含旧 Kafka package；
- `sdk/go/v0.2.0` 包含旧 Logging package；
- `sdk/go/v0.3.0` 包含旧 Gateway package；
- `sdk/go/v0.4.0` 包含旧 Object Storage 和 `storagecontract` package。

同时使用父 SDK与任一独立 Module 时，必须把父 SDK原子升级到 `v0.5.0`，不能通过长期 `replace` 绕过边界：

```sh
go get \
  github.com/L1ndenbaum/stellarmesh-sdk/sdk/go@v0.5.0 \
  github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/objectstorage@v0.1.0 \
  github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/gateway@v0.3.0 \
  github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging@v0.3.0 \
  github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/mq/kafka@v0.1.0
go mod tidy
```

业务仓库只安装实际使用的 Module，不应因为需要一个小工具而复制上述完整命令。升级前阅读[发布与版本引用](../../release.md)，并在业务仓库执行自己的格式、静态检查、测试和部署契约验证。
