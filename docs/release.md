# 发布与版本引用

## 发布边界

同一个 Git commit 可以产生多类制品，但每类制品使用独立 tag，避免无关组件的版本互相阻断：

- 根 tag `vX.Y.Z` 只触发四个容器镜像构建并推送到 GHCR；
- 父 Go Module tag `sdk/go/vX.Y.Z` 触发 logging、gateway 和 objectstorage 的公共 Go proxy 消费验证；
- Kafka Go Module tag `sdk/go/mq/kafka/vX.Y.Z` 触发 Kafka-only 公共消费与依赖边界验证；
- Python 日志组件 tag `sdk/python/logging/vX.Y.Z` 只构建并发布 `stellarmesh-logging`；
- Python Storage 组件 tag `sdk/python/storage/vX.Y.Z` 只构建并发布 `stellarmesh-storage`；
- `contracts/logging/v1/` 与 `contracts/storage/v1/` 随源码版本发布，SDK、服务、sink 和迁移制品必须来自经过完整验证的 commit。

不同组件可以有不同版本。例如四个镜像可以保持 `0.1.1`，父 Go SDK 提升到 `0.2.0`，Kafka Module 独立从 `0.1.0` 开始，Python 日志包保持 `0.1.2`，`stellarmesh-storage` 继续保持 `0.1.0`。发布前必须先让 `main` 或 `dev` 分支的持续验证通过，不得用 tag 绕过格式、静态检查、测试、镜像构建或集成测试。已经推送的 tag 不得移动、删除、覆盖或强推；制品内容需要修改时必须提升 patch 版本。

## 镜像发布

推送形如 `v0.1.1` 的根 tag 后，`.github/workflows/release-images.yml` 会发布：

- `ghcr.io/l1ndenbaum/stellarmesh-sdk/logging-service`；
- `ghcr.io/l1ndenbaum/stellarmesh-sdk/storage-service`；
- `ghcr.io/l1ndenbaum/stellarmesh-sdk/logging-clickhouse-sink`；
- `ghcr.io/l1ndenbaum/stellarmesh-sdk/logging-clickhouse-migrate`。

工作流同时生成完整版本、`major.minor`、提交 SHA tag、构建来源证明和 SBOM，并发布 `linux/amd64`、`linux/arm64` manifest。上述 GHCR package 为公开制品，开发机、CI 和生产服务器可以匿名拉取，不需要在 Compose 或镜像中保存 GitHub 凭据。若未来重新设为私有，必须改用仅有 `read:packages` 的独立只读身份。

业务仓库可以用完整版本 tag 完成测试，但生产清单必须把验证后的镜像解析为 digest，并以 `image@sha256:...` 形式交给 `server-infrastructure`。四个镜像的 digest 应记录在同一份发布清单中，迁移镜像仍只作为一次性任务运行。

## Go Module 发布

父 SDK 与 Kafka SDK 是两个独立 Module，都不能通过根 tag 发布。确认发布 commit 后分别创建与 Module 目录匹配的 tag：

```sh
git tag -a sdk/go/mq/kafka/v0.1.0 -m '发布 Kafka Go SDK v0.1.0'
git push origin sdk/go/mq/kafka/v0.1.0

git tag -a sdk/go/v0.2.0 -m '发布 Go SDK v0.2.0'
git push origin sdk/go/v0.2.0
```

`.github/workflows/release-go.yml` 只接受上述两个硬编码 tag 前缀，不检出仓库源码。它会清除私有 Module 配置，在限定时间内等待 `proxy.golang.org` 收录，并使用官方 checksum database 从全新的外部 Module 执行对应验证：父 SDK 编译 logging、`slog.Handler`、gateway 和 objectstorage；Kafka Module 构造 PLAIN、SCRAM-256、SCRAM-512 连接，并编译 Publisher 与 Topic 检查 API。Kafka-only 消费者的依赖图不得出现 AWS SDK、Chi、JWT 或 Redis。

仓库持续验证会把父 SDK 与 Kafka Module 移到物理隔离的临时目录，分别以 `GOWORK=off go test ./...` 测试，并另外编译 Kafka-only 与组合消费者。组合测试用于阻止父 SDK 旧版本和新 Kafka Module 同时提供相同 package。

只需要 Kafka 的业务项目固定引入：

```sh
GOWORK=off go get github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/mq/kafka@v0.1.0
go mod tidy
```

同时使用父 SDK 与 Kafka 的项目必须原子升级：

```sh
GOWORK=off go get \
  github.com/L1ndenbaum/stellarmesh-sdk/sdk/go@v0.2.0 \
  github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/mq/kafka@v0.1.0
go mod tidy
```

`sdk/go/v0.1.1` 仍不可变地包含旧 `mq/kafka` package，不能与新 Kafka Module 同时使用，否则可能产生 `ambiguous import`。公共消费验证不得添加本地 `replace`，并应确保 `GOPRIVATE`、`GONOPROXY` 和 `GONOSUMDB` 没有把这两个公开 Module 排除在公共代理或校验数据库以外。

## Python SDK 发布

`.github/workflows/release-python.yml` 只接受两个显式组件 tag，并通过硬编码白名单选择包路径和 distribution。根版本 tag 不触发 Python 发布，未知前缀不能注入任意构建路径。

以日志包 `0.1.2` 为例：

```sh
git tag -a sdk/python/logging/v0.1.2 -m '发布 stellarmesh-logging v0.1.2'
git push origin sdk/python/logging/v0.1.2
```

build job 会校验 tag 版本与对应 `pyproject.toml` 一致，运行 Ruff、mypy、pytest，构建一次 wheel 与 sdist，并执行 `twine check`。后续发布任务不检出源码，只下载同一份 Actions artifact：

1. `testpypi` Environment 通过 Trusted Publisher 把制品发布到 TestPyPI；
2. TestPyPI 成功后，`pypi` Environment 等待人工审批；
3. 审批通过后，通过正式 PyPI Trusted Publisher 发布完全相同的制品。

只有两个 publish job 拥有 `id-token: write`，仓库不创建或保存 PyPI API token。GitHub Environment 与 Trusted Publisher 必须精确使用以下值：

| 平台 | owner | repository | workflow | environment |
| --- | --- | --- | --- | --- |
| TestPyPI | `L1ndenbaum` | `stellarmesh-sdk` | `release-python.yml` | `testpypi` |
| PyPI | `L1ndenbaum` | `stellarmesh-sdk` | `release-python.yml` | `pypi` |

`pypi` Environment 应启用人工审批。首次发布尚不存在的包时，应在创建组件 tag 前配置 pending Trusted Publisher，并再次确认 distribution 名称仍可用。已有 `stellarmesh-storage` TestPyPI Trusted Publisher 也必须把 workflow 文件名更新为 `release-python.yml`，否则后续 Storage 组件 tag 无法获得 OIDC 发布身份。配置方式见 [PyPA GitHub Actions 发布指南](https://packaging.python.org/en/latest/guides/publishing-package-distribution-releases-using-github-actions-ci-cd-workflows/) 和 [PyPI Trusted Publisher 故障说明](https://docs.pypi.org/trusted-publishers/troubleshooting/)。

TestPyPI 验证时应先从正式 PyPI 安装第三方依赖，再单独安装目标制品，避免 TestPyPI 参与 `httpx`、`pydantic` 等依赖解析：

```sh
python -m pip install 'httpx>=0.27,<1' 'pydantic>=2.7,<3'
python -m pip install --no-deps \
  --index-url https://test.pypi.org/simple \
  stellarmesh-logging==0.1.2
```

正式发布完成后，生产项目只使用默认 PyPI 或团队批准的内部索引，不把 TestPyPI 当作长期依赖源。

## `0.1.1` 与 Python `0.1.2` 发布顺序

Go SDK 和四个镜像使用 `0.1.1`。Python 日志包的 `sdk/python/logging/v0.1.1` 在发布前构建检查阶段失败，未上传到 TestPyPI 或 PyPI；该 tag 保持不可变。修复发布检查后，Python 日志包单独提升为 `0.1.2`：

```sh
git tag -a sdk/go/v0.1.1 -m '发布 Go SDK v0.1.1'
git tag -a v0.1.1 -m '发布 Stellarmesh 镜像 v0.1.1'
git push origin sdk/go/v0.1.1
git push origin v0.1.1

git tag -a sdk/python/logging/v0.1.2 -m '发布 stellarmesh-logging v0.1.2'
git push origin sdk/python/logging/v0.1.2
```

创建前必须用远端查询确认目标 tag 不存在，并确认 `dev` 的“持续验证”工作流已经成功。Python 外部发布配置尚未完成时不能先推送 Python 组件 tag；否则 OIDC 失败后只能修复外部配置并重跑同一不可变 commit 的 workflow，不能改写 tag。若必须修改 workflow、源码或制品内容，则提升 Python 包 patch 版本并创建新 tag。

## Kafka `0.1.0` 与父 SDK `0.2.0` 发布顺序

Kafka Module 从父 `sdk/go` 拆出后，父 SDK 使用 `0.2.0` 表达 Module 边界变化，Kafka Module 首次发布为 `0.1.0`。本次不创建根 tag，因此不会重新构建或发布四个 `0.1.1` 镜像，也不创建 Python 组件 tag。

发布必须来自同一个已经通过持续验证的 commit，并按以下顺序执行：

1. 确认远端不存在 `sdk/go/mq/kafka/v0.1.0` 和 `sdk/go/v0.2.0`；
2. 创建并推送 `sdk/go/mq/kafka/v0.1.0`；
3. 等待公共 Proxy、checksum database 和 Kafka-only workflow 全部验证成功；
4. 创建并推送 `sdk/go/v0.2.0`；
5. 等待父 SDK workflow 成功；
6. 在没有 `replace` 的全新外部 Module 中同时引用两者，完成最终组合消费测试。

如果必须修改 Kafka Module 源码或发布 workflow，Kafka 版本提升到 `v0.1.1`；如果必须修改父 SDK 内容，父版本提升到 `v0.2.1`。任何已经推送的 tag 都不能移动、删除或复用。

## 发布后验证

发布完成后至少确认：

1. 父 Go SDK `0.2.0`、Kafka Module `0.1.0` 已发布；四个镜像保持 `0.1.1`，失败的 Python `0.1.1` tag 保持不可变，`stellarmesh-storage` 仍为 `0.1.0`；
2. 从 TestPyPI 和正式 PyPI 的全新 Python 3.11 环境安装 `stellarmesh-logging==0.1.2`，验证 import、版本元数据、严格 service 校验与标准 `logging.Handler`；
3. 从公共 `GOPROXY` 和 `GOSUMDB` 获取父 `sdk/go/v0.2.0` 与 Kafka `sdk/go/mq/kafka/v0.1.0`，不使用 `replace`，分别完成 Kafka-only、父 SDK 和组合消费测试；
4. 使用空临时 `DOCKER_CONFIG` 匿名拉取四个 `0.1.1` 镜像，确认双架构 manifest、provenance 和 SBOM，并记录不可变 digest；
5. 使用临时合法认证文件启动 `logging-service:0.1.1`，在 Kafka 不可用但 spool 可写时确认服务可以降级接收；
6. 业务环境已预先创建源 Topic、DLQ Topic、ClickHouse database、对象存储 Bucket 和最小权限身份；
7. 先运行迁移制品，再启动 sink 和 ingester；
8. 日志服务与 `storage-service` 的 `/health/ready`、`/metrics` 均符合预期；
9. 有效事件落库、无效消息进入 DLQ、预签名上传下载和 namespace 授权均通过最小端到端验证；
10. 回滚时不自动执行 down migration，先按发布清单和数据备份策略处理。
