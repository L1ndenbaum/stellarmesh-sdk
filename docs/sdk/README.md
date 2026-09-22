# SDK 选择与接入

先按需求选组件，再打开指南中的固定版本安装与最小示例。当前已发布版本只在[发布矩阵](../release.md#当前制品矩阵)维护；主干新增能力不代表 registry 已发布。

| 需求 | 组件与接入指南 | 前置条件 |
| --- | --- | --- |
| 浏览器／Node 声明式 HTTP、SSE | [前端 SDK](frontend/README.md) | ESM；浏览器流使用 Fetch |
| Go 环境配置、JSON 解码、HTTP server | [父 Go SDK](go/README.md) | Go 1.24 |
| 路由、认证、限流与反向代理 | [Go Gateway](go/gateway.md) | 项目提供路由与安全策略 |
| Cookie 会话管理与踢下线 | [Redis Session](go/sessionauth.md) | **主干能力，尚未发布**；Redis 单实例或 Sentinel |
| Go 安全结构化日志 | [Go Logging](go/logging.md) | 标准库 `log/slog`，无第三方运行依赖 |
| Kafka 连接、发布与 Topic 检查 | [Go Kafka](go/kafka.md) | 外部 Kafka，项目管理 Topic／ACL |
| Go 进程内对象存储 | [Go Object Storage](go/object-storage.md) | S3／MinIO 项目凭据 |
| Python 安全日志格式化 | [Python Logging](python/README.md) | Python 3.11，标准库 `logging` |
| Python 对象上传下载 | [Python Storage](python/storage.md) | Python 3.11，Storage 控制面和 S3／MinIO |
| 项目对象存储控制面 | [Storage 服务](../storage-service.md) | 外部 Bucket／Policy／凭据与部署编排 |

## 日志默认路线

标准库 Logger 输出结构化单行 JSON，由业务项目选择 Collector、缓冲策略及数据库投影。日志包不发送 HTTP，不持有 service token，不规定 Topic 或审计模型。完整边界见[架构说明](../sdk-content.md#轻量日志组件)，组合步骤见[日志接入](../sdk-integration.md#日志接入)。

旧 Logging v1/v2 与旧运行时仅为迁移冻结保留；新项目不要依赖它们。事务性审计应写入业务数据库或 outbox。

## 对象存储路线

持有凭据的 Go 进程可直接用 Object Storage Module。其他客户端通过项目级 Storage 服务签发请求，再直接与 S3／MinIO 传输字节。协议以 [Storage v1](../../contracts/storage/v1/README.md) 为准；安装、组合、部署分别由组件指南、[跨组件接入](../sdk-integration.md#对象存储接入)和[服务指南](../storage-service.md)说明。
