# Stellarmesh SDK

本仓库提供跨项目复用的基础 SDK、声明式 Go 网关、统一日志平台和项目级对象存储控制面。业务项目只负责引用 SDK 与声明自己的路由、namespace、策略及部署参数；本仓库不提供 Docker Compose、环境变量文件或生产资源编排。

## 仓库内容

- `contracts/logging/v1/`：日志事件、DLQ v1/v2 记录、尺寸限制与 HTTP OpenAPI 契约。
- `contracts/storage/v1/`：Storage 控制面 OpenAPI、访问配置 Schema、共享限制与测试数据。
- `sdk/go/`：Go 公共 HTTP、环境配置和 namespace 绑定的进程内对象存储 SDK。
- `sdk/go/gateway/`：独立发布的 fail-close 声明式 Gateway、JWT 认证与 Redis 限流 Module。
- `sdk/go/gateway/loggingadapter/`：开发中的可选 Stellarmesh Logging 访问日志适配器，当前尚未发布。
- `sdk/go/logging/`：独立发布、仅依赖标准库的 Logging v1 契约、异步客户端与 `slog.Handler`。
- `sdk/go/mq/kafka/`：独立发布的轻量 Kafka Go Module，提供 PLAIN、SCRAM、TLS/mTLS、Publisher 和 Topic 检查。
- `sdk/python/logging/`：独立发布的 `stellarmesh-logging` 日志包。
- `sdk/python/storage/`：独立发布的 `stellarmesh-storage` 同步与异步对象存储客户端。
- `services/logging/`：接收 HTTP 日志并发布到 Kafka 的常驻服务。
- `services/storage/`：签发 S3/MinIO 预签名请求的项目级控制面服务，不代理对象字节。
- `sinks/logging/clickhouse/`：消费 Kafka 并写入 ClickHouse 的日志落库服务，以及独立迁移镜像。

详细说明见[SDK 内容](docs/sdk-content.md)，语言 SDK 的使用方法见[SDK 接入教程](docs/sdk/README.md)，Go 日志接入见[Go Logging SDK](docs/sdk/go/logging.md)，Kafka 接入见[Go Kafka SDK](docs/sdk/go/kafka.md)，网关接入见[Go 网关 SDK](docs/sdk/go/gateway.md)，对象存储服务部署见[storage-service 部署与权限边界](docs/storage-service.md)，平台服务与业务项目的整体接入步骤见[接入 SDK](docs/sdk-integration.md)，版本与不可变制品规则见[发布与版本引用](docs/release.md)。

## 本地验证

```sh
make bootstrap
make format
make verify
make race
make images
make integration
```

`make verify` 会执行 Go 格式检查、`go vet`、Go 测试、两个 Python 项目的 Ruff、mypy、pytest、Shell 语法检查与 `git diff --check`。`make race` 运行全部 Go 竞态检查。`make images` 会构建日志接收服务、storage-service、ClickHouse sink 和迁移制品四个镜像；`make integration` 依次验证日志落库与 DLQ 流程、MinIO 最小权限、预签名直传、Multipart、版本删除、readiness 故障恢复和优雅关闭。测试结束后清理临时容器、网络和 Secret，不要求仓库提供 Compose。

## 生产责任边界

本仓库拥有协议、应用代码、`log_events` 表结构及迁移源码。生产环境中的 ClickHouse database、用户、权限，Kafka Topic、principal、ACL，以及对象存储 Bucket、Policy、CORS、Versioning、Lifecycle、Secret、镜像 digest、迁移时机和发布顺序由业务部署或 `server-infrastructure` 声明和编排。常驻服务不得持有管理员或迁移凭据，不会自动创建 Bucket，也不会在启动时自动执行数据库迁移。
