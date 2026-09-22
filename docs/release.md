# 发布与版本引用

## 当前制品矩阵

各组件独立版本，不要求数字一致：

| 制品 | 当前已发布版本 | 说明 |
| --- | --- | --- |
| 前端 HTTP／SSE SDK | `sdk/frontend/v0.3.0` | `@stellarmesh/sdk@0.3.0`，ESM，MIT |
| 父 Go SDK | `sdk/go/v0.5.0` | 标准库 HTTP 与环境配置基础能力 |
| Go Object Storage | `sdk/go/objectstorage/v0.1.0` | namespace 绑定的对象存储能力 |
| Go Gateway Core | `sdk/go/gateway/v0.3.1` | 通用 `slog` 访问日志，保留限流结果 |
| Go Kafka | `sdk/go/mq/kafka/v0.1.0` | 轻量 Kafka 连接与 Publisher |
| Python Storage | `sdk/python/storage/v0.1.1` | `stellarmesh-storage==0.1.1` |
| storage-service | 根镜像 tag `v0.3.0` | Storage v1，支持 pretty／JSON 与日志级别 |
| Go Logging | `sdk/go/logging/v0.4.0` | `slog.Handler`安全装饰器 |
| Python Logging | `sdk/python/logging/v0.5.0` | `stellarmesh-logging==0.5.0` Pretty／JSON Formatter |
| 旧 Gateway Logging Adapter | `0.2.0` | 冻结的远程日志适配器，只供迁移 |
| 旧 Logging 运行时镜像 | 根镜像 tag `v0.2.0` | 最后版本，不再构建新版本 |

旧 tag 和已经发布的 PyPI/GHCR 制品永久保持不可变。版本内容需要修改时必须提升版本，不能移动、删除、覆盖或强推已经发布的 tag。2026-09-22 只读核对了官方 npm、PyPI 元数据及远端组件 tag；镜像摘要沿用已记录的发布验收，本轮没有重新拉取镜像。Gateway `sessionauth`／凭证提取扩展仍为主干能力，未包含在当前 Gateway 制品中。

历史拆分和兼容记录见[历史发布记录](releases/history.md)。

## 前端 npm 发布流程

包归属 `stellarmesh`，采用 MIT，仅公开 ESM 包根入口。主干目前采用 tsdown 构建；已发布版本的制品内容以对应 tag 和历史记录为准，不能用同版本重新上传。

以下命令以已经发布的 `0.3.0` 说明制品命名；下一次正式发布必须先提升源码及锁文件版本并统一替换命令中的版本。仅准备本地制品不会发布，也不会创建 tag。

### 准备并验证唯一制品

使用 Node 24 和 npm，在仓库根目录先执行全仓验证 `make verify`。准备前确认目标源码已经提交，工作区干净，再执行以下步骤；正式上传前须推送已验证源码并通过要求的 CI：

```sh
cd sdk/frontend
npm ci --registry=https://registry.npmjs.org/
npx playwright install --with-deps chromium
npm run release:prepare -- sdk/frontend/v0.3.0
```

`release:prepare` 校验包名、MIT、正式版本号、锁文件根元数据及公开 registry 配置；依次完成格式／静态／类型检查、行为测试、干净构建、Chromium 验证，再打包一次并验证这份 tarball。每次成功输出一个独立的 `.artifacts/0.3.0-随机后缀/` 目录，包含：

- `stellarmesh-sdk-0.3.0.tgz`：通过验证的 npm 制品；
- `release.json`：包名、版本、文件名、SHA-512 integrity、SHA-256、registry、公开访问权限与 `latest` 标签。

目录被 Git、Biome 和 ESLint 忽略，也不会进入 npm 包。失败时清理本次不完整制品，不覆盖以前的成功结果。可传入组件 tag 做版本一致性校验，例如 `npm run release:prepare -- sdk/frontend/v0.3.0`；这不会创建 tag。当前命令只接受正式版本号，不支持预发布版本与标签的自动选择。

`npm run build` 每次清除旧 `dist/`，避免已删除模块残留。`npm pack` 的 `prepack` 只执行干净构建；完整发布检查由 `release:prepare` 负责，避免消费验证触发打包后递归验证。消费验证安装时禁用生命周期脚本，并检查包清单、许可证、版本、ESM 与公开类型契约。验证已有制品时使用：

```sh
npm run test:consumer -- /绝对路径/stellarmesh-sdk-0.3.0.tgz
```

已有制品路径不会触发源码重建，验证前后检查其内容摘要一致。

[手动准备工作流](../.github/workflows/prepare-frontend.yml)使用相同命令，只允许 `workflow_dispatch` 手动触发，上传 `frontend-npm` 制品并保留 7 天。它只有仓库只读权限，不包含发布步骤。工作流需要先推送到默认分支才能在 Actions 页面手动运行；本地通过验证不代表 GitHub Actions 已运行成功。

### 人工发布

正式发布时再登录 npm 官方 registry，确认身份输出为 `l1ndenbaum`，并完成 npm 要求的账号验证与 2FA。发布账号需要拥有 `stellarmesh` 组织及包的发布权限。`publishConfig` 固定为官方 registry 和 `public`，命令仍显式指定，避免本机镜像源影响发布。

以下步骤会向 npm 上传包，应在正式发布时执行。将 `release_dir` 替换为本次成功准备命令输出的目录，不重新运行打包生成替代文件：

```sh
npm login --registry=https://registry.npmjs.org/
npm whoami --registry=https://registry.npmjs.org/
release_dir='/绝对路径/sdk/frontend/.artifacts/0.3.0-随机后缀'
npm publish "$release_dir/stellarmesh-sdk-0.3.0.tgz" \
  --ignore-scripts --registry=https://registry.npmjs.org/ --access public --tag latest --browser=false
```

上传前核对 tarball 的 SHA-256 与 `release.json` 一致；发布失败先确认 registry 是否已经存在该版本，不能直接修改内容后复用相同版本。发布后查询真实元数据：

```sh
npm view @stellarmesh/sdk@0.3.0 \
  name version license dist.integrity --json --registry=https://registry.npmjs.org/
```

将 `dist.integrity` 与准备时保存的 SHA-512 比较，再从官方 registry 下载该版本到新临时目录，使用 `test:consumer` 验证下载的 tarball，并在全新业务消费目录安装 `@stellarmesh/sdk@0.3.0` 验证公开安装。全部通过后，记录源码 commit、创建指向该 commit 的不可变组件 tag `sdk/frontend/v0.3.0`，更新已发布矩阵。当前没有前端 tag 发布触发器，根 `vX.Y.Z` tag 仍只用于镜像。

### 后续自动发布

首次发布成功后，再在 npm 包设置中配置 GitHub Actions Trusted Publishing，绑定本仓库及将来独立的发布工作流。随后增加组件 tag 校验、制品下载与摘要复核、OIDC 发布和真实 registry 消费验证。当前准备工作流没有 OIDC 写权限，也不存储 npm token；不应把它当成已经接通的自动发布流程。

接入与兼容边界见[前端 HTTP SDK](sdk/frontend/README.md)。

## 当前日志方向

SDK 不再发布公共 `logging-service`、ClickHouse sink 或迁移镜像。新项目使用语言标准库，本地可选择 pretty，采集时输出结构化单行 JSON，再由项目自己的 Vector 等 Collector 完成持久缓冲、重放和数据库投影。日志表、字段映射、保留策略和数据库 migration 属于业务项目，不属于公共 SDK。

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

## 历史入口

<a id="前端-sse-sdk-030-已发布"></a>
<a id="前端-http-sdk-020-正式发布"></a>
<a id="前端-http-sdk-首次-npm-发布"></a>
<a id="python-logging-050-与-storage-service-030"></a>
<a id="logging-040-与-gateway-031"></a>

旧发布入口保留于此。历史版本的源码、CI 结果、摘要与豁免记录见[历史发布记录](releases/history.md)；新的发布操作见[前端 npm 发布流程](#前端-npm-发布流程)。
