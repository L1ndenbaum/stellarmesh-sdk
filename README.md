# Stellarmesh SDK

本仓库提供跨项目复用的基础 SDK、统一日志协议、日志接收服务、ClickHouse 落盘服务和版本化迁移制品。业务项目只负责引用 SDK 与声明自己的部署参数；本仓库不提供 Docker Compose、环境变量文件或生产资源编排。

## 仓库内容

- `contracts/logging/v1/`：日志事件、DLQ v1/v2 记录、尺寸限制与 HTTP OpenAPI 契约。
- `sdk/go/`：Go 公共基础能力和异步日志客户端。
- `sdk/python/`：Python 异步日志客户端、标准 `logging.Handler` 适配器与日志门面。
- `services/logging/`：接收 HTTP 日志并发布到 Kafka 的常驻服务。
- `sinks/clickhouse/`：消费 Kafka 并写入 ClickHouse 的常驻服务，以及独立迁移镜像。

详细说明见[SDK 内容](docs/sdk-content.md)，语言 SDK 的使用方法见[SDK 接入教程](docs/sdk/README.md)，平台服务与业务项目的整体接入步骤见[接入 SDK 与日志平台](docs/sdk-integration.md)，版本与不可变制品规则见[发布与版本引用](docs/release.md)。

## 本地验证

```sh
make bootstrap
make format
make verify
make race
make images
make integration
```

`make verify` 会执行 Go 格式检查、`go vet`、Go 测试、Ruff、mypy、pytest、Shell 语法检查与 `git diff --check`。`make race` 运行全部 Go 竞态检查。`make images` 会构建日志接收服务、ClickHouse sink 和迁移制品三个镜像；`make integration` 额外使用临时 Docker network 验证有效事件落库与坏消息进入 DLQ，测试结束后清理临时容器和网络，不要求仓库提供 Compose。

## 生产责任边界

本仓库拥有协议、应用代码、`log_events` 表结构及迁移源码。生产环境中的 ClickHouse database、用户、权限，Kafka Topic、principal、ACL，以及镜像 digest、迁移时机和发布顺序由 `server-infrastructure` 统一声明和编排。常驻服务不得持有管理员或迁移凭据，也不会在启动时自动执行迁移。
