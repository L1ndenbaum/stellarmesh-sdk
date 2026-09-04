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
| 旧 Go/Python Logging | `0.2.0` | 冻结的远程 Logging v2 客户端，只供迁移 |
| 旧 Gateway Logging Adapter | `0.2.0` | 冻结的远程日志适配器，只供迁移 |
| 旧 Logging 运行时镜像 | 根镜像 tag `v0.2.0` | 最后版本，不再构建新版本 |

旧 tag 和已经发布的 PyPI/GHCR 制品永久保持不可变。版本内容需要修改时必须提升 patch，不能移动、删除、覆盖或强推已经发布的 tag。历史拆分和兼容记录见[历史发布记录](releases/history.md)。

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
