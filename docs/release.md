# 发布与版本引用

## 当前制品矩阵

各组件独立版本，不要求数字一致：

| 制品 | 当前已发布版本 | 说明 |
| --- | --- | --- |
| 前端 HTTP SDK | `sdk/frontend/v0.1.0` | `@stellarmesh/sdk@0.1.0`，ESM，MIT |
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

旧 tag 和已经发布的 PyPI/GHCR 制品永久保持不可变。版本内容需要修改时必须提升版本，不能移动、删除、覆盖或强推已经发布的 tag。历史拆分和兼容记录见[历史发布记录](releases/history.md)。

## 前端 HTTP SDK `0.2.0` 发布准备

源码及锁文件版本已提升至 `0.2.0`，新增根入口类型 `ErrorCodeExtractor` 与不可变派生方法 `withErrorCodeExtractor`。默认继续使用 `0.1.0` 的 `code` 提取和声明式 API；项目可独立提取额外错误码，真实 HTTP 状态仍保留在 `error.status`。空返回值清空错误码，提取器配置失败保留响应诊断并停止认证恢复与重试。

本次不升级运行时依赖，不修改三字段 `ApiEnvelope<T>`，不定义业务错误码，也不增加会话刷新或通知机制。使用方式及兼容细节见[错误码提取](sdk/frontend/README.md#可配置错误码提取)。发布目标为官方 registry 的 `@stellarmesh/sdk@0.2.0`、公开访问和 `latest`；此节是发布准备，实际发布验收后再更新制品矩阵和摘要。

2026-09-13 已验证并推送源码 commit `65ff52dddabc59d8c704603417af09fdce2337da`。本地 `make verify`、129 个前端测试、Chromium 实际 HTTP 与隔离 tarball 消费全部通过；`release:prepare -- sdk/frontend/v0.2.0` 生成并验证唯一制品 `stellarmesh-sdk-0.2.0.tgz`，摘要如下：

```text
SHA-512 integrity: sha512-k1bs5J6fqVQ2ITuqZl0nuWIWRwsHBbnsqsM6WqQ6FURlzZb6U/PdA64OHFsUxChwadd1lO56FWTnmpxHq29UKA==
SHA-256: a9528fdfadae60d30f85c16eb902d19759571cb2cf7943a6109ae9098e691059
```

对应源码的 [GitHub CI](https://github.com/L1ndenbaum/stellarmesh-sdk/actions/runs/34704294630) 中，前端 HTTP SDK、Go 模块、Python Logging、Python Storage 和 Shell 检查均通过。Storage 集成拉取 `minio/minio@sha256:a1ea29fa28355559ef137d71fc570e508a214ec84ff8083e39bc5428980b015e` 时返回 `pull access denied`，导致汇总检查失败。该失败位于既有 Storage 集成环境，不属于前端包的运行或构建依赖。

当前发布因上述 CI 失败暂停，尚未上传 npm 或创建 `sdk/frontend/v0.2.0` tag；官方制品下载与空缓存公开安装验收尚未执行。发布须等待该检查恢复，或取得本次前端发布对该检查的明确豁免，不能将已准备制品视为正式发布。继续发布时使用上述已验证制品，不重新打包替换。

## 前端 HTTP SDK 首次 npm 发布

前端包为 `@stellarmesh/sdk`，首次发布版本 `0.1.0`，采用 MIT 许可证，许可证位于 `sdk/frontend/LICENSE` 并随 npm 包分发。该许可证针对前端包，不改变其他语言模块的许可声明。已于 2026-09-12 发布到官方 npm registry，公开包为 [`@stellarmesh/sdk@0.1.0`](https://www.npmjs.com/package/@stellarmesh/sdk/v/0.1.0)。包归属 npm 组织 `stellarmesh`，由具备组织发布权限的账号维护。

包只提供 ESM JavaScript 和类型声明，唯一公开入口是包根；发布清单只包含 `dist/`、`package.json`、`README.md` 和 `LICENSE`。运行时依赖仍为锁定版本的 Axios。首次发布不升级依赖，组件 tag 为 `sdk/frontend/v0.1.0`。

认证装配已统一为 `createAuthSession` 与 `withAuth(auth, bindingOptions)`，支持显式恢复策略和 Cookie Session。使用前阅读[认证接口迁移](sdk/frontend/README.md#未发布初版的认证接口迁移)及项目凭证条件提交示例。

首次发布源码 commit 为 `b89936671c17abd4a7eda30e79236e266025e0fa`，包含大写 `HttpErrorKind.HTTP` 等运行时常量及同名类型。发布上传的是一次构建并验证的 tarball，官方 registry 匿名下载的制品与本地 SHA-512／SHA-256 一致，公开 tarball 的 ESM 和 TypeScript 消费验证通过；随后在全新目录使用空 npm 缓存按包名安装 `@stellarmesh/sdk@0.1.0`，确认大写错误常量、声明式入口与安装摘要一致，当时的 `latest` 指向 `0.1.0`。

```text
SHA-256: 2233314a4a24cfa776ebf9cd0ea94ba5aee4960119722e6a65e44abea7a74c98
```

本地 `make verify`、104 个前端测试、Chromium 与隔离消费验证通过。对应源码的 [GitHub CI](https://github.com/L1ndenbaum/stellarmesh-sdk/actions/runs/34675639098) 中前端任务已通过；Storage 集成因拉取外部 MinIO 镜像被拒绝而失败，不能将本次记录视为整条 CI 成功。该集成不属于前端 npm 包的运行或构建依赖。

### 准备并验证唯一制品

使用 Node 24 和 npm，在仓库根目录先执行全仓验证 `make verify`。准备前确认目标源码已经提交，工作区干净，再执行以下步骤；正式上传前须推送已验证源码并通过要求的 CI：

```sh
cd sdk/frontend
npm ci --registry=https://registry.npmjs.org/
npx playwright install --with-deps chromium
npm run release:prepare -- sdk/frontend/v0.2.0
```

`release:prepare` 校验包名、MIT、正式版本号、锁文件根元数据及公开 registry 配置；依次完成格式／静态／类型检查、行为测试、干净构建、Chromium 验证，再打包一次并验证这份 tarball。每次成功输出一个独立的 `.artifacts/0.2.0-随机后缀/` 目录，包含：

- `stellarmesh-sdk-0.2.0.tgz`：通过验证的 npm 制品；
- `release.json`：包名、版本、文件名、SHA-512 integrity、SHA-256、registry、公开访问权限与 `latest` 标签。

目录被 Git、Biome 和 ESLint 忽略，也不会进入 npm 包。失败时清理本次不完整制品，不覆盖以前的成功结果。可传入组件 tag 做版本一致性校验，例如 `npm run release:prepare -- sdk/frontend/v0.2.0`；这不会创建 tag。当前命令只接受正式版本号，不支持预发布版本与标签的自动选择。

`npm run build` 每次清除旧 `dist/`，避免已删除模块残留。`npm pack` 的 `prepack` 只执行干净构建；完整发布检查由 `release:prepare` 负责，避免消费验证触发打包后递归验证。消费验证安装时禁用生命周期脚本，并检查包清单、许可证、版本、ESM 与公开类型契约。验证已有制品时使用：

```sh
npm run test:consumer -- /绝对路径/stellarmesh-sdk-0.2.0.tgz
```

已有制品路径不会触发源码重建，验证前后检查其内容摘要一致。

[手动准备工作流](../.github/workflows/prepare-frontend.yml)使用相同命令，只允许 `workflow_dispatch` 手动触发，上传 `frontend-npm` 制品并保留 7 天。它只有仓库只读权限，不包含发布步骤。工作流需要先推送到默认分支才能在 Actions 页面手动运行；本地通过验证不代表 GitHub Actions 已运行成功。

### 人工发布

正式发布时再登录 npm 官方 registry，确认身份输出为 `l1ndenbaum`，并完成 npm 要求的账号验证与 2FA。发布账号需要拥有 `stellarmesh` 组织及包的发布权限。`publishConfig` 固定为官方 registry 和 `public`，命令仍显式指定，避免本机镜像源影响发布。

以下步骤会向 npm 上传包，应在正式发布时执行。将 `release_dir` 替换为本次成功准备命令输出的目录，不重新运行打包生成替代文件：

```sh
npm login --registry=https://registry.npmjs.org/
npm whoami --registry=https://registry.npmjs.org/
release_dir='/绝对路径/sdk/frontend/.artifacts/0.2.0-随机后缀'
npm publish "$release_dir/stellarmesh-sdk-0.2.0.tgz" \
  --ignore-scripts --registry=https://registry.npmjs.org/ --access public --tag latest
```

上传前核对 tarball 的 SHA-256 与 `release.json` 一致；发布失败先确认 registry 是否已经存在该版本，不能直接修改内容后复用相同版本。发布后查询真实元数据：

```sh
npm view @stellarmesh/sdk@0.2.0 \
  name version license dist.integrity --json --registry=https://registry.npmjs.org/
```

将 `dist.integrity` 与准备时保存的 SHA-512 比较，再从官方 registry 下载该版本到新临时目录，使用 `test:consumer` 验证下载的 tarball，并在全新业务消费目录安装 `@stellarmesh/sdk@0.2.0` 验证公开安装。全部通过后，记录源码 commit、创建指向该 commit 的不可变组件 tag `sdk/frontend/v0.2.0`，更新已发布矩阵。当前没有前端 tag 发布触发器，根 `vX.Y.Z` tag 仍只用于镜像。

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

## Python Logging `0.5.0` 与 storage-service `0.3.0`

两个制品已从源码 `7c08afe519d73db528c7b3010c780c28b8313fce` 正式发布：

- Python Logging `0.5.0` 新增 `PrettyFormatter`，与 JSON 共用字段清洗、预算及异常处理，不修改现有 JSON 输出契约。公开安装命令为 `python -m pip install stellarmesh-logging==0.5.0`。
- storage-service `0.3.0` 读取应用层 `LOG_LEVEL` 与 `LOG_FORMAT`，默认 `info`／`pretty`，采集环境应显式使用 `json`；标准库 Text／JSON Handler 共用 Go Logging `0.4.0` 的安全装饰器。启动、退出及 HTTP 服务错误明确使用 ERROR 级别。
- Go Logging 保持 `0.4.0`，Object Storage SDK、旧日志运行时镜像和其他组件未重新发布。

[Python 发布工作流](https://github.com/L1ndenbaum/stellarmesh-sdk/actions/runs/34023002746)已完成构建、TestPyPI 与正式 PyPI；[镜像发布工作流](https://github.com/L1ndenbaum/stellarmesh-sdk/actions/runs/34023002871)已完成验证、双架构镜像、SBOM 与 provenance。

公开镜像固定引用为：

```text
ghcr.io/l1ndenbaum/stellarmesh-sdk/storage-service@sha256:fbc59a34a34072b4c5f800e36553312ff2b711b4ba309ffed074b83ec1b13a33
```

发布后已在全新 Python 环境从正式 PyPI 安装并验证版本元数据、两种 Formatter 与脱敏；使用空临时 `DOCKER_CONFIG` 匿名检查、拉取公开镜像，确认 `linux/amd64`、`linux/arm64` manifest，并使用该公开镜像通过 Storage v1／MinIO 集成与 pretty／JSON 启动错误检查。上述为公开制品与本地容器验证，不代表业务生产环境已部署或 Collector 链路已验收。

## Logging `0.4.0` 与 Gateway `0.3.1`

三个组件已从同一源码 `2494919ce5d84d2710267959ca1ebf8fc7577469` 发布，组件 tag 均保持不可变：

- Go Logging：`sdk/go/logging/v0.4.0`；
- Python Logging：`sdk/python/logging/v0.4.0`，正式 PyPI 安装 `stellarmesh-logging==0.4.0`；
- Gateway Core：`sdk/go/gateway/v0.3.1`。

Logging 本次收窄自动类型展开、改为精确敏感字段匹配、统一分组与容器预算，并删除 Go 的两个 panic 错误类别。它是破坏性变更，迁移步骤见[Go 教程](sdk/go/logging.md)、[Python 教程](sdk/python/README.md)和[共享清洗约定](../contracts/logging/sanitization.md)。

Gateway 修复访问日志复制时丢失 `RateLimitResult` 的问题，保留已执行限流阶段的结果；日志副本的 map 和 Roles 与原始状态隔离。公开接口和鉴权、限流决策不变。

源码持续验证、两个 Go tag 的公共消费者工作流，以及 Python 构建、TestPyPI、正式 PyPI 工作流均已通过。发布后另在全新环境验证公共 Go Proxy/checksum database 消费及正式 PyPI 安装、版本元数据、公开格式化 API 和嵌套脱敏输出。本次没有创建根 tag 或重新发布镜像。

未来发布仍须先推送已验证源码并等待持续验证成功，再确认目标 tag 不存在、创建组件 tag。Python 使用同一份构建 artifact 依次发布 TestPyPI 与正式 PyPI；发布后验证真实制品，再更新已发布矩阵。

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
