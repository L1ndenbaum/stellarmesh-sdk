# 发布与版本引用

## 当前制品矩阵

本轮重构从同一组已验证提交发布以下制品。各组件独立版本，不要求数字一致：

| 制品 | 版本或 tag | 说明 |
| --- | --- | --- |
| 父 Go SDK | `sdk/go/v0.5.0` | 只保留标准库实现的环境配置、JSON 请求解码和 HTTP server 基础能力 |
| Go Object Storage | `sdk/go/objectstorage/v0.1.0` | provider-neutral 对象模型与 AWS S3/S3-compatible 适配器 |
| Go Gateway | `sdk/go/gateway/v0.3.0` | 通用 Gateway、默认 `slog` 访问日志、JWT 与 Redis 限流 |
| Gateway Logging Adapter | `sdk/go/gateway/loggingadapter/v0.1.0` | Gateway 到 Stellarmesh Logging 的可选适配器 |
| Go Logging | `sdk/go/logging/v0.1.0` | 保持不变 |
| Go Kafka | `sdk/go/mq/kafka/v0.1.0` | 保持不变 |
| Python Logging | `stellarmesh-logging==0.1.2` | 保持不变，不重复发布 |
| Python Storage | `sdk/python/storage/v0.1.1` / `stellarmesh-storage==0.1.1` | 同一制品先发布 TestPyPI，再发布 PyPI |
| 四个运行时镜像 | 根 tag `v0.1.2` | `logging-service`、`storage-service`、ClickHouse sink 与 migrate |

旧 tag 永远保持不可变。版本内容需要修改时必须提升 patch，不能移动、删除、覆盖或强推已经发布的 tag。历史拆分和兼容记录见[历史发布记录](releases/history.md)。

## Tag 与制品边界

- 根 tag `vX.Y.Z` 只触发四个 GHCR 镜像；
- `sdk/go/vX.Y.Z` 只验证父 Go Module 的公共消费；
- `sdk/go/objectstorage/vX.Y.Z`、`sdk/go/gateway/vX.Y.Z`、`sdk/go/gateway/loggingadapter/vX.Y.Z`、`sdk/go/logging/vX.Y.Z` 和 `sdk/go/mq/kafka/vX.Y.Z` 分别发布对应嵌套 Module；
- `sdk/python/logging/vX.Y.Z` 与 `sdk/python/storage/vX.Y.Z` 分别发布对应 Python distribution；
- Go 与 Python 的组件 tag 不触发镜像构建，根 tag 也不触发 Python 发布。

发布工作流必须从公共 Go Proxy 或实际构建出的 wheel/sdist 验证制品，而不是依赖仓库 `go.work`、本地 `replace` 或可变源码目录。四个 GHCR package 为公开制品，可以匿名拉取；生产环境仍应固定已验证的 manifest digest。

## Go Module 发布顺序

本轮存在显式依赖顺序：父 SDK 与 Object Storage 必须先可从公共代理获取，内部服务才能记录真实 checksum；Gateway 必须先发布，Logging Adapter 才能记录并消费 Gateway `v0.3.0`。

1. 在 `dev` 上运行完整验证并等待“持续验证”成功；
2. 确认远端不存在 `sdk/go/objectstorage/v0.1.0`、`sdk/go/v0.5.0`、`sdk/go/gateway/v0.3.0` 和 `sdk/go/gateway/loggingadapter/v0.1.0`；
3. 创建并推送 Object Storage tag，等待公共代理和 checksum database 收录；
4. 创建并推送父 SDK tag，等待公共消费验证成功；
5. 创建并推送 Gateway tag，等待公共消费验证成功；
6. 对 `services/logging`、`services/storage`、`sinks/logging/clickhouse` 和 `sdk/go/gateway/loggingadapter` 执行 `GOWORK=off go mod tidy`，记录真实公开 checksum；
7. 提交 checksum，推送 `dev` 并再次等待持续验证成功；
8. 创建并推送 Logging Adapter tag，等待公共消费验证成功。

带注释的 tag 命令如下：

```sh
git tag -a sdk/go/objectstorage/v0.1.0 -m '发布 Object Storage Go SDK v0.1.0'
git push origin sdk/go/objectstorage/v0.1.0

git tag -a sdk/go/v0.5.0 -m '发布 Go SDK v0.5.0'
git push origin sdk/go/v0.5.0

git tag -a sdk/go/gateway/v0.3.0 -m '发布 Gateway Go SDK v0.3.0'
git push origin sdk/go/gateway/v0.3.0

git tag -a sdk/go/gateway/loggingadapter/v0.1.0 \
  -m '发布 Gateway Logging Adapter v0.1.0'
git push origin sdk/go/gateway/loggingadapter/v0.1.0
```

父 SDK `v0.4.0` 仍包含 Object Storage 和旧 `storagecontract`；它不能与独立 Object Storage Module 同时进入同一个 build list。迁移项目必须把父 SDK 原子升级到 `v0.5.0`。更早版本中的 Gateway、Logging 和 Kafka 重复包遵循同一规则，不能用长期 `replace` 掩盖 `ambiguous import`。

## Python 发布

Python 项目使用各自的 `uv.lock`。组件 tag 与 `pyproject.toml` 版本必须一致。build job 以 uv 0.12.5 冻结同步，运行 Ruff、mypy、pytest、`uv pip check`，构建一次 wheel/sdist 并执行 `twine check`；TestPyPI 与 PyPI job 只下载同一份 Actions artifact，不检出源码，也不保存 API token。

本轮只创建 Storage tag：

```sh
git tag -a sdk/python/storage/v0.1.1 -m '发布 stellarmesh-storage v0.1.1'
git push origin sdk/python/storage/v0.1.1
```

TestPyPI 使用 `testpypi` Environment，正式 PyPI 使用 `pypi` Environment 和人工审批；两个平台的 Trusted Publisher 都必须精确绑定 `.github/workflows/release-python.yml` 与对应 Environment。Python Logging 保持 `0.1.2`，不能用新 tag 重复上传同一版本。

## 镜像发布

确认所有 Go Module checksum 已回填、Python Storage 已发布并且 `dev` 持续验证成功后，创建根 tag：

```sh
git tag -a v0.1.2 -m '发布 Stellarmesh 运行时镜像 v0.1.2'
git push origin v0.1.2
```

工作流发布以下公开镜像，并生成 `0.1.2`、`0.1` 和 commit SHA 标签：

- `ghcr.io/l1ndenbaum/stellarmesh-sdk/logging-service`；
- `ghcr.io/l1ndenbaum/stellarmesh-sdk/storage-service`；
- `ghcr.io/l1ndenbaum/stellarmesh-sdk/logging-clickhouse-sink`；
- `ghcr.io/l1ndenbaum/stellarmesh-sdk/logging-clickhouse-migrate`。

每个镜像必须包含 `linux/amd64`、`linux/arm64` manifest、provenance 和 SBOM。常驻服务镜像不执行 Schema 或 Bucket 迁移；migrate 镜像只由外部编排器使用迁移身份一次性运行。

## 发布后验证

发布完成后必须从干净环境验证真实制品：

1. 清空 Go 私有模块环境变量，使用 `https://proxy.golang.org` 和 `sum.golang.org`，不添加 `replace`，分别运行父 SDK、Object Storage、Gateway、Logging Adapter、Logging 和 Kafka 的受版本控制 consumer fixture；
2. 在全新 Python 3.11 环境从正式 PyPI 安装 `stellarmesh-storage==0.1.1`，验证 import、严格模型、同步与异步客户端构造；TestPyPI 只用于确认同一 artifact 的预发布链路；
3. 使用空临时 `DOCKER_CONFIG` 匿名检查并拉取四个 `0.1.2` 镜像，记录不可变 manifest digest；
4. 使用最小合法配置实际启动 `logging-service` 和 `storage-service`，验证健康检查、Kafka/spool 与 MinIO readiness；
5. 最终确认 `dev` 与远端同步、`git diff --check` 通过且工作区干净。

Actions runner 或公共代理的临时故障可以在同一不可变 commit 上重跑。只要需要修改源码、工作流、锁文件或制品内容，就必须使用新的 patch 版本。
