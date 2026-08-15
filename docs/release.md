# 发布与版本引用

## 发布边界

一次正式发布以同一个 Git commit 为准，但不同制品使用各自适合的版本入口：

- 根 tag `vX.Y.Z` 触发四个容器镜像构建并推送到 GHCR；
- Go 子模块使用 `sdk/go/vX.Y.Z` tag；
- `stellarmesh-logging` 和 `stellarmesh-storage` Python 包从同一 commit 分别构建；当前受保护的测试发布流程只把 `stellarmesh-storage` 发布到 TestPyPI；
- `contracts/logging/v1/` 与 `contracts/storage/v1/` 随源码 tag 发布，SDK、服务、sink 和迁移制品必须来自同一 commit。

根 tag 与 Go 子模块 tag 可以指向同一 commit。发布前必须先让 `main` 或 `dev` 分支的持续验证通过，不得用 tag 绕过格式、静态检查、测试、镜像构建或集成测试。

## 镜像发布

推送形如 `v0.1.0` 的根 tag 后，`.github/workflows/release-images.yml` 会发布：

- `ghcr.io/<组织>/stellarmesh-sdk/logging-service`；
- `ghcr.io/<组织>/stellarmesh-sdk/storage-service`；
- `ghcr.io/<组织>/stellarmesh-sdk/logging-clickhouse-sink`；
- `ghcr.io/<组织>/stellarmesh-sdk/logging-clickhouse-migrate`。

工作流同时生成版本 tag、提交短 SHA tag、构建来源证明和 SBOM。镜像默认保持私有；业务仓库、开发机和生产服务器必须使用仅有 `read:packages` 权限的身份拉取，不能把 GHCR 凭据写入 Compose、镜像或 Git。业务仓库可以用版本 tag 完成测试，但生产清单必须把验证后的镜像解析为 digest，并以 `image@sha256:...` 形式交给 `server-infrastructure`。四个镜像的 digest 应记录在同一份发布清单中，迁移镜像仍只作为一次性任务运行。

## Go SDK 发布

Go module 位于 `sdk/go` 子目录，因此不能只创建根 tag。确认发布 commit 后创建对应子模块 tag：

```sh
git tag -a sdk/go/v0.1.0 -m '发布 Go SDK v0.1.0'
git push origin sdk/go/v0.1.0
```

业务项目随后固定引入：

```sh
go get github.com/L1ndenbaum/stellarmesh-sdk/sdk/go@v0.1.0
go mod tidy
```

## Python SDK 测试发布

Python 包名分别为 `stellarmesh-logging` 和 `stellarmesh-storage`。二者独立发布、独立安装，但版本必须指向同一验证 commit。

当前 `.github/workflows/release-python-test.yml` 只发布 `stellarmesh-storage`。根 tag 触发后，工作流先校验 tag 与包版本一致，运行格式、静态检查和测试，再构建 wheel 与源码包。发布任务绑定 `testpypi` Environment，并通过 Trusted Publisher 的短期 OIDC 身份上传，不保存长期 API token。

```sh
python -m build sdk/python/storage
```

测试业务仓库应先从正式 PyPI 安装第三方依赖，再单独从 TestPyPI 安装固定版本，避免 TestPyPI 参与第三方依赖解析：

```sh
python -m pip install --no-deps \
  --index-url https://test.pypi.org/simple \
  stellarmesh-storage==0.1.0
```

TestPyPI 只用于验证构建、Trusted Publisher 和跨仓库接入流程，不是生产依赖源。正式部署前必须把相同制品晋级到正式 PyPI 或团队管理的内部 registry；生产镜像不能直接安装 TestPyPI 包或可变分支。`stellarmesh-logging` 仍由后续独立发布流程处理。

## 发布后验证

发布完成后至少确认：

1. 已发布的 Go module tag、Python 包和四个镜像都对应同一 commit；
2. 四个镜像可以按 digest 拉取，并包含预期架构；
3. 业务环境已预先创建源 Topic、DLQ Topic、ClickHouse database、对象存储 Bucket 和最小权限身份；
4. 先运行迁移制品，再启动 sink 和 ingester；
5. 日志服务与 `storage-service` 的 `/health/ready`、`/metrics` 均符合预期；
6. 有效事件落库、无效消息进入 DLQ、预签名上传下载和 namespace 授权均通过最小端到端验证；
7. 回滚时不自动执行 down migration，先按发布清单和数据备份策略处理。
