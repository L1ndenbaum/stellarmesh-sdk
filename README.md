# Stellarmesh SDK

本仓库提供跨项目复用的轻量 SDK、声明式 Go 网关和项目级对象存储控制面。日志默认路线是语言标准库输出结构化单行 JSON，再由业务项目选择 Vector 等 Collector 和自己的数据库投影；本仓库不再提供公共日志运行时。业务项目负责声明自己的路由、namespace、少量日志字段约定及部署参数；本仓库不提供 Docker Compose、环境变量文件或生产资源编排。

## 仓库内容

- `contracts/logging/v1/`、`contracts/logging/v2/`：只读冻结的历史远程日志契约，仅供仍运行 `0.2.0` 的项目迁移。
- `contracts/storage/v1/`：Storage 控制面 OpenAPI、访问配置 Schema、共享限制与测试数据。
- `sdk/go/`：只依赖标准库的 Go HTTP、server 与环境配置基础能力。
- `sdk/go/objectstorage/`：独立发布、namespace 绑定的进程内对象存储 Module。
- `sdk/go/gateway/`：独立发布的 fail-close 声明式 Gateway、JWT 认证与 Redis 限流 Module。
- `sdk/go/gateway/loggingadapter/`：独立发布的可选 Stellarmesh Logging 访问日志适配器。
- `sdk/go/logging/`：独立发布、仅依赖标准库的 Logging v2 契约、异步客户端与 `slog.Handler`。
- `sdk/go/mq/kafka/`：独立发布的轻量 Kafka Go Module，提供 PLAIN、SCRAM、TLS/mTLS、Publisher 和 Topic 检查。
- `sdk/python/logging/`：独立发布的 `stellarmesh-logging` 日志包。
- `sdk/python/storage/`：独立发布的 `stellarmesh-storage` 同步与异步对象存储客户端。
- `services/storage/`：签发 S3/MinIO 预签名请求的项目级控制面服务，不代理对象字节。

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

`make bootstrap` 按两个独立 `uv.lock` 创建 Python 3.11 环境。`make verify` 会执行 Go 格式检查、`go vet`、Go 测试、两个 Python 项目的 Ruff、mypy、pytest、依赖兼容检查、Shell 语法检查与 `git diff --check`。`make race` 运行全部 Go 竞态检查。`make images` 构建 storage-service，`make integration` 验证 MinIO 最小权限、预签名直传、Multipart、版本删除、readiness 故障恢复和优雅关闭。测试结束后清理临时容器、网络和 Secret，不要求仓库提供 Compose。

## 生产责任边界

本仓库拥有 SDK 与 Storage v1 协议。日志表、解析规则、保留策略、Collector 配置及其数据库凭据由采用该能力的业务项目拥有；生产资源和迁移执行由业务部署或 `server-infrastructure` 编排。对象存储 Bucket、Policy、CORS、Versioning、Lifecycle、Secret、镜像 digest、迁移时机和发布顺序同样不属于 SDK。常驻服务不得持有管理员或迁移凭据，不会自动创建 Bucket，也不会在启动时自动执行数据库迁移。
