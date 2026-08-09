# 发布与版本引用

## 发布边界

一次正式发布以同一个 Git commit 为准，但不同制品使用各自适合的版本入口：

- 根 tag `vX.Y.Z` 触发三个容器镜像构建并推送到 GHCR；
- Go 子模块使用 `sdk/go/vX.Y.Z` tag；
- Python 包从同一 commit 构建，并发布到团队管理的内部 Python registry；
- `contracts/logging/v1/` 随源码 tag 发布，SDK、ingester、sink 和迁移制品必须来自同一 commit。

根 tag 与 Go 子模块 tag 可以指向同一 commit。发布前必须先让 `main` 或 `dev` 分支的持续验证通过，不得用 tag 绕过格式、静态检查、测试、镜像构建或集成测试。

## 镜像发布

推送形如 `v0.1.0` 的根 tag 后，`.github/workflows/release-images.yml` 会发布：

- `ghcr.io/<组织>/stellarmesh-logging-service`；
- `ghcr.io/<组织>/stellarmesh-logging-clickhouse-sink`；
- `ghcr.io/<组织>/stellarmesh-logging-clickhouse-migrate`。

工作流同时生成版本 tag、提交短 SHA tag、构建来源证明和 SBOM。业务仓库可以用版本 tag 完成测试，但生产清单必须把验证后的镜像解析为 digest，并以 `image@sha256:...` 形式交给 `server-infrastructure`。三个镜像的 digest 应记录在同一份发布清单中，迁移镜像仍只作为一次性任务运行。

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

## Python SDK 发布

Python 包名为 `stellarmesh-logging`。本仓库的镜像发布工作流不会假定团队使用哪一种 Python registry，也不会持有 registry 凭据。发布者应在已经通过持续验证的 commit 上构建 wheel 和源码包，再由受保护的发布任务上传：

```sh
python -m build sdk/python
```

业务仓库应从内部 registry 固定安装 `stellarmesh-logging==X.Y.Z`，不要在生产镜像中直接安装可变分支。

## 发布后验证

发布完成后至少确认：

1. Go module tag、Python 包和三个镜像都对应同一 commit；
2. 三个镜像可以按 digest 拉取，并包含预期架构；
3. 业务环境已预先创建源 Topic、DLQ Topic、ClickHouse database 和最小权限身份；
4. 先运行迁移制品，再启动 sink 和 ingester；
5. `/health/ready`、`/metrics`、有效事件落库和无效消息进入 DLQ 均符合预期；
6. 回滚时不自动执行 down migration，先按发布清单和数据备份策略处理。
