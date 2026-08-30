# 发布与版本引用

## 当前制品矩阵

本轮从经过完整验证的提交发布 Logging v2。各组件独立版本，不要求数字一致：

| 制品 | 版本或 tag | 说明 |
| --- | --- | --- |
| 父 Go SDK | `sdk/go/v0.5.0` | 保持不变；标准库 HTTP 与环境配置基础能力 |
| Go Object Storage | `sdk/go/objectstorage/v0.1.0` | 保持不变 |
| Go Gateway Core | `sdk/go/gateway/v0.3.0` | 保持不变；通用 `slog` 访问日志 |
| Gateway Logging Adapter | `sdk/go/gateway/loggingadapter/v0.2.0` | 适配 Logging v2，并始终产生 `kind=LOG` |
| Go Logging | `sdk/go/logging/v0.2.0` | Logging v2 事件、客户端、结构化门面和 `slog.Handler` |
| Go Kafka | `sdk/go/mq/kafka/v0.1.0` | 保持不变 |
| Python Logging | `sdk/python/logging/v0.2.0` / `stellarmesh-logging==0.2.0` | 同一制品先发布 TestPyPI，再发布 PyPI |
| Python Storage | `sdk/python/storage/v0.1.1` / `stellarmesh-storage==0.1.1` | 保持不变 |
| 四个运行时镜像 | 根 tag `v0.2.0` | Logging v2 service、sink、migrate，以及未改变 Storage 协议的 storage-service |

旧 tag 永远保持不可变。版本内容需要修改时必须提升 patch，不能移动、删除、覆盖或强推已经发布的 tag。历史拆分和兼容记录见[历史发布记录](releases/history.md)。

## Tag 与制品边界

- 根 tag `vX.Y.Z` 只触发四个 GHCR 镜像；
- `sdk/go/vX.Y.Z` 只验证父 Go Module；
- `sdk/go/objectstorage/vX.Y.Z`、`sdk/go/gateway/vX.Y.Z`、`sdk/go/gateway/loggingadapter/vX.Y.Z`、`sdk/go/logging/vX.Y.Z` 和 `sdk/go/mq/kafka/vX.Y.Z` 分别发布对应嵌套 Module；
- `sdk/python/logging/vX.Y.Z` 与 `sdk/python/storage/vX.Y.Z` 分别发布对应 Python distribution；
- Go 与 Python 组件 tag 不触发镜像构建，根 tag 也不触发 Python 发布。

发布工作流必须从公共 Go Proxy 或实际构建出的 wheel/sdist 验证制品，不能依赖仓库 `go.work`、本地 `replace` 或可变源码目录。四个 GHCR package 为公开制品，可以匿名拉取；生产环境仍应固定已验证的 manifest digest。

## Logging v2 发布顺序

Adapter、logging-service 和 ClickHouse sink 依赖尚未发布的 Go Logging `v0.2.0`。发布必须分成两个经过持续验证的 commit：

1. 完成 Logging v2 契约、SDK、service、sink、迁移、测试与文档，在 `dev` 运行完整验证并等待“持续验证”成功；
2. 确认远端不存在 `sdk/go/logging/v0.2.0`、`sdk/go/gateway/loggingadapter/v0.2.0`、`sdk/python/logging/v0.2.0` 和 `v0.2.0`；
3. 创建并推送 Go Logging tag，等待公共 Go Proxy、checksum database 和公开消费者 smoke 成功；
4. 在 Adapter、logging-service 和 ClickHouse sink 中运行 `GOWORK=off go mod tidy`，记录 Logging `v0.2.0` 的真实 checksum；
5. 提交 checksum，推送 `dev` 并再次等待持续验证成功；
6. 在 checksum commit 创建并推送 Adapter 与 Python Logging tag；
7. 等待 Adapter 公共消费验证、TestPyPI 发布和正式 PyPI 人工审批发布全部成功；
8. 创建根 `v0.2.0` tag，发布四个多架构镜像；
9. 从全新环境验证全部公开制品和已发布镜像的 Logging v2 端到端链路。

带注释的 tag 命令如下：

```sh
git tag -a sdk/go/logging/v0.2.0 -m '发布 Logging Go SDK v0.2.0'
git push origin sdk/go/logging/v0.2.0

git tag -a sdk/go/gateway/loggingadapter/v0.2.0 \
  -m '发布 Gateway Logging Adapter v0.2.0'
git push origin sdk/go/gateway/loggingadapter/v0.2.0

git tag -a sdk/python/logging/v0.2.0 \
  -m '发布 stellarmesh-logging v0.2.0'
git push origin sdk/python/logging/v0.2.0

git tag -a v0.2.0 -m '发布 Stellarmesh Logging v2 运行时镜像'
git push origin v0.2.0
```

Python build job 使用 uv 冻结同步，运行 Ruff、mypy、pytest、`uv pip check`，构建一次 wheel/sdist 并执行元数据检查。TestPyPI 与 PyPI job 只下载同一 Actions artifact；只有 publish job 拥有 `id-token: write`。两个平台的 Trusted Publisher 必须精确绑定 `.github/workflows/release-python.yml` 与各自的 `testpypi`、`pypi` Environment，正式 PyPI Environment 保留人工审批。

## v1 到 v2 的部署边界

v2 运行时不提供 v1 HTTP、Kafka、spool 或 decoder 兼容。发布新制品不代表业务项目可以直接滚动升级；每个项目必须在维护窗口执行：

1. 停止 v1 producers，排空客户端、logging-service 队列、v1 Kafka lag 与 v1 spool；
2. 备份 ClickHouse，检查表大小、分区与 `distinct level`；
3. 创建 `stellarmesh.logging.events.v2`、`stellarmesh.logging.events.v2.dlq` 及相应 ACL；
4. 运行 `logging-clickhouse-migrate:0.2.0` 升级到 revision 2；
5. 按 v2 sink、v2 logging-service、v2 producers 的顺序启动并验证；
6. 回滚时先排空 v2，再执行 down migration，并整体恢复 v1，不允许只回滚单个组件。

迁移 up/down 都会执行同步 mutation，可能扫描并改写历史分区。未知历史 `level` 或 `kind` 会 fail-close；迁移失败必须阻止 sink 和 service 发布。v1 非空 spool 不会被 v2 自动 quarantine，必须在切换前排空或由运维移出数据目录。

## 镜像发布

根 `v0.2.0` 工作流发布以下公开镜像，并生成 `0.2.0`、`0.2` 和 commit SHA 标签：

- `ghcr.io/l1ndenbaum/stellarmesh-sdk/logging-service`；
- `ghcr.io/l1ndenbaum/stellarmesh-sdk/storage-service`；
- `ghcr.io/l1ndenbaum/stellarmesh-sdk/logging-clickhouse-sink`；
- `ghcr.io/l1ndenbaum/stellarmesh-sdk/logging-clickhouse-migrate`。

每个镜像必须包含 `linux/amd64`、`linux/arm64` manifest、provenance 和 SBOM。统一发布列车会给未改变协议的 storage-service 同时生成 `0.2.0` 标签；这不表示 Storage v1 协议或 Python Storage 包升级。常驻服务镜像不执行 Schema 或 Bucket 迁移，migrate 镜像只由外部编排器使用迁移身份一次性运行。

## 发布后验证

发布完成后必须从干净环境验证真实制品：

1. 清空 Go 私有 Module 环境变量，使用 `https://proxy.golang.org` 与 `sum.golang.org`，不添加 `replace`，运行 Logging `v0.2.0` 和 Adapter `v0.2.0` 的受版本控制 consumer fixture；
2. 在全新 Python 3.11 环境从正式 PyPI 安装 `stellarmesh-logging==0.2.0`，验证版本、严格 kind/level 模型、标准 Handler 和结构化审计入口；
3. 使用空临时 `DOCKER_CONFIG` 匿名检查并拉取四个 `0.2.0` 镜像，记录不可变 manifest digest、双架构、provenance 和 SBOM；
4. 使用已发布的 service、sink 和 migrate 镜像完成 `LOG+INFO`、`LOG+ERROR`、`AUDIT+INFO`、Kafka 中断期间 priority spool、DLQ、ClickHouse kind/level 与 readiness 恢复验证；
5. 验证 storage-service 镜像仍能完成既有 Storage v1 集成；
6. 最终确认 `dev` 与远端同步、`git diff --check` 通过且工作区干净。

Actions runner、PyPI 审批或公共代理的临时问题可以在同一不可变 commit 上重跑或等待。只要必须修改源码、workflow、锁文件或制品内容，对应组件就必须提升 patch，不能复用已经推送的 tag。
