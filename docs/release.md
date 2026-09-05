# 发布与版本引用

## 当前制品矩阵

各组件独立版本，不要求数字一致：

| 制品 | 当前已发布版本 | 说明 |
| --- | --- | --- |
| 父 Go SDK | `sdk/go/v0.5.0` | 标准库 HTTP 与环境配置基础能力 |
| Go Object Storage | `sdk/go/objectstorage/v0.1.0` | namespace 绑定的对象存储能力 |
| Go Gateway Core | `sdk/go/gateway/v0.3.0` | 通用 `slog` 访问日志 |
| Go Kafka | `sdk/go/mq/kafka/v0.1.0` | 轻量 Kafka 连接与 Publisher |
| Python Storage | `sdk/python/storage/v0.1.1` | `stellarmesh-storage==0.1.1` |
| storage-service | 根镜像 tag `v0.2.0` | 当前公开 Storage v1 镜像 |
| Go Logging | `sdk/go/logging/v0.3.0` | `slog.Handler`安全装饰器 |
| Python Logging | `sdk/python/logging/v0.3.0` | `stellarmesh-logging==0.3.0` JSON Formatter |
| 旧 Gateway Logging Adapter | `0.2.0` | 冻结的远程日志适配器，只供迁移 |
| 旧 Logging 运行时镜像 | 根镜像 tag `v0.2.0` | 最后版本，不再构建新版本 |

旧 tag 和已经发布的 PyPI/GHCR 制品永久保持不可变。版本内容需要修改时必须提升版本，不能移动、删除、覆盖或强推已经发布的 tag。历史拆分和兼容记录见[历史发布记录](releases/history.md)。

## 当前日志方向

SDK 不再发布公共 `logging-service`、ClickHouse sink 或迁移镜像。新项目使用语言标准库输出结构化单行 JSON，再由项目自己的 Vector 等 Collector 完成持久缓冲、重放和数据库投影。日志表、字段映射、保留策略和数据库 migration 属于业务项目，不属于公共 SDK。

`contracts/logging/v1`、`contracts/logging/v2` 与 Logging `0.2.0` 制品暂时冻结一个迁移周期。它们不是新项目的接入标准，也不会随新的轻量日志包继续演进。

## Tag 与制品边界

- 根 tag `vX.Y.Z` 只构建并发布 `ghcr.io/l1ndenbaum/stellarmesh-sdk/storage-service`；
- `sdk/go/vX.Y.Z` 只验证父 Go Module；
- `sdk/go/objectstorage/vX.Y.Z`、`sdk/go/gateway/vX.Y.Z`、`sdk/go/logging/vX.Y.Z` 和 `sdk/go/mq/kafka/vX.Y.Z` 分别发布对应嵌套 Module；
- `sdk/python/logging/vX.Y.Z` 与 `sdk/python/storage/vX.Y.Z` 分别发布对应 Python distribution；
- Go 与 Python组件 tag 不触发镜像构建，根 tag 也不触发 Python 发布。

发布工作流必须从公共 Go Proxy 或实际构建出的 wheel/sdist验证制品，不能依赖仓库 `go.work`、本地 `replace` 或可变源码目录。公开 GHCR 镜像可以匿名拉取；生产环境仍应固定已验证的 manifest digest。

## 待发布的 Logging `0.4.0`

主干准备 Go Logging `sdk/go/logging/v0.4.0` 与 Python Logging `sdk/python/logging/v0.4.0`，尚未创建或推送 tag。上方矩阵继续表示实际已发布版本；修改源码版本不等于制品已发布。

本次收窄自动类型展开、改为精确敏感字段匹配、统一分组与容器预算，并删除 Go 的两个 panic 错误类别。它是破坏性变更，迁移步骤见[Go 教程](sdk/go/logging.md)、[Python 教程](sdk/python/README.md)和[共享清洗约定](../contracts/logging/sanitization.md)。仅修复兼容行为时提升 patch；缩减公开行为时提升 minor，不覆盖任何既有 tag。

未来发布时，先推送经过完整验证的源码并等待持续验证成功，确认目标 tag 不存在后，再从同一 commit 创建两个组件 tag。Go tag 验证公共代理制品和共享清洗样例；Python tag 构建一次 wheel/sdist，检查通过后使用同一 artifact 依次发布 TestPyPI 与正式 PyPI。发布后验证真实制品，再更新已发布矩阵。不创建根 tag、不重发日志镜像。

## 待发布的 Gateway `0.3.1`

主干准备 `sdk/go/gateway/v0.3.1`，修复访问日志复制时丢失 `RateLimitResult` 的问题。已执行限流阶段的 `allowed`、`rejected`、`error` 或 `disabled` 结果将正常输出，尚未执行的阶段不补造结果；日志副本的 map 和 Roles 与原始状态隔离。公开接口和鉴权、限流决策不变。该版本尚未创建或推送 tag，发布后再更新上方矩阵。

## 旧日志制品边界

以下历史制品仍可按原版本引用，但不再接收新功能或重建：

- `ghcr.io/l1ndenbaum/stellarmesh-sdk/logging-service:0.2.0`；
- `ghcr.io/l1ndenbaum/stellarmesh-sdk/logging-clickhouse-sink:0.2.0`；
- `ghcr.io/l1ndenbaum/stellarmesh-sdk/logging-clickhouse-migrate:0.2.0`；
- `github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging@v0.2.0`；
- `github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/gateway/loggingadapter@v0.2.0`；
- `stellarmesh-logging==0.2.0`。

仍使用这些制品的项目必须在自己的迁移窗口内排空旧客户端队列、服务 spool、Kafka lag 和 DLQ，再切换 Collector 路线。强事务审计不能依赖这条普通日志链路，应使用业务数据库或 transactional outbox。

## storage-service 镜像发布

根 tag 工作流只发布：

```text
ghcr.io/l1ndenbaum/stellarmesh-sdk/storage-service
```

工作流生成完整版本、`major.minor` 和 commit SHA 标签，并发布 `linux/amd64`、`linux/arm64` manifest、provenance 与 SBOM。常驻服务不执行 Schema 或 Bucket 迁移；对象存储资源和生产迁移由外部编排器负责。

## 发布后验证

发布完成后必须从干净环境验证真实制品：

1. Go Module 使用 `https://proxy.golang.org` 与 `sum.golang.org`，不添加本地 `replace`；
2. Python 包从正式 PyPI 安装并验证公开 API；
3. 使用空临时 `DOCKER_CONFIG` 匿名拉取 storage-service，记录不可变 manifest digest与架构；
4. 使用已发布 storage-service 镜像完成 Storage v1 集成；
5. 确认 `dev` 与远端同步、`git diff --check` 通过且工作区干净。

Actions runner、PyPI 审批或公共代理的临时问题可以在同一不可变 commit上重跑或等待。只要必须修改源码、workflow、锁文件或制品内容，对应组件就必须提升 patch，不能复用已经推送的 tag。
