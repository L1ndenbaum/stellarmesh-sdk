# 历史发布记录

> 本文件保存旧版本拆分、失败处理和迁移背景。文中的“当前”“未发布”和版本矩阵均表示记录写入时的状态，不是现行发布指令；实际操作以[当前发布文档](../release.md)为准。

## Python Object Storage 0.1.0 已发布

2026-09-22 已通过 Trusted Publishing 将 `stellarmesh-objectstorage==0.1.0` 发布到 [TestPyPI](https://test.pypi.org/project/stellarmesh-objectstorage/0.1.0/) 和 [PyPI](https://pypi.org/project/stellarmesh-objectstorage/0.1.0/)。同步使用 Boto3，异步使用 aioboto3，提供绑定 Bucket／Prefix 的对象传输、预签名和显式 Multipart；旧 `stellarmesh-storage`、Storage 服务及 Go SDK 版本保持不变。

发布源码为 `1ccfb6f2e24cbe01ccdca377089150cec5d4f94d`，对应 annotated tag `sdk/python/objectstorage/v0.1.0`。该源码的[持续验证](https://github.com/L1ndenbaum/stellarmesh-sdk/actions/runs/35731749484)全部通过，包含新包的 Python 3.11、真实 MinIO 与制品消费检查；本地全仓 `make verify` 也通过。

[发布工作流](https://github.com/L1ndenbaum/stellarmesh-sdk/actions/runs/35735245772)构建唯一 wheel／sdist，完成 Twine、公开注释与隔离消费验证后，先发布 TestPyPI，核对制品并完成仓库既有 environment review，再将同一 Actions artifact 提升至 PyPI，未重新构建。

| 制品 | SHA-256 |
| --- | --- |
| `stellarmesh_objectstorage-0.1.0-py3-none-any.whl` | `111be7bb6d1aed7ea508ca98be43816154786a73180d0169ebb5f8f6e6d07d02` |
| `stellarmesh_objectstorage-0.1.0.tar.gz` | `9782e3336f7b7e260e4cb9adfc84faf5b89f2848727dec8be061389dc51230b5` |

发布后从官方 PyPI 匿名下载上述两个文件，摘要与 Actions／TestPyPI 一致；全新虚拟环境使用空缓存、官方索引按包名安装成功，公开导入、同步／异步签名与关闭行为通过。公开 wheel 再次通过严格类型消费与 README／docstring 保留检查。真实 MinIO 验证不代表生产 AWS 权限或业务模型验收。

## 前端 SSE SDK 0.3.0 已发布

2026-09-21 已发布 `@stellarmesh/sdk@0.3.0`，npm `latest` 指向该版本。新增声明式 `http.sse.get/post/request`、增量 SSE 解析、共享 AuthSession 恢复和流生命周期管理；普通 HTTP 与上传继续使用 Axios，依赖版本保持不变。

发布源码为 `f6efb868c7d6fdaa3c54b8b68c7b795efd2dbbfc`，组件 annotated tag 为 [`sdk/frontend/v0.3.0`](https://github.com/L1ndenbaum/stellarmesh-sdk/releases/tag/sdk/frontend/v0.3.0)。本地 `make verify`、164 个前端测试、Chromium 实际 SSE／Cookie／上传验证和隔离 tarball 消费均通过；该源码的[持续验证](https://github.com/L1ndenbaum/stellarmesh-sdk/actions/runs/35600466699)全部通过，包含 Go／Redis、Python、Storage 集成与前端，没有使用旧版本的 CI 豁免。

`release:prepare -- sdk/frontend/v0.3.0` 生成并验证唯一制品，上传同一份 tarball，未重建替换：

```text
SHA-256: acfd64ac9895d78acf9ddcebf810ab243f6ea3bd7a26b66760461399797a8f4b
SHA-512: bIFiShFZzf5m1n12SFMYGmbGvoUAKuZj9vnd4jDla3jHEG3canpBvAKGpL1HEGU3LhvyID+1wfO21wYCmiimcQ==
```

发布后已核对官方 registry 版本、`latest` 和 integrity；匿名下载的公开 tarball 与上述摘要一致，通过 ESM、SSE 运行时及公开 TypeScript 契约验证。另在全新目录使用空缓存、空认证配置按包名安装正式版本并验证 SSE，安装 integrity 与发布制品相同。

本机曾出现 `xdg-open` 等待 Chrome 退出，使 npm 在浏览器验证成功后仍等待启动器。此时尚未最终上传；确认 registry 无该版本后结束等待会话，使用同一制品及 `--browser=false` 重新完成发布验证。后续手工发布默认采用该选项，在已有浏览器打开 npm 给出的链接，不让 CLI 等待浏览器进程退出。

## 前端 HTTP SDK `0.2.0` 正式发布

源码及锁文件版本已提升至 `0.2.0`，新增根入口类型 `ErrorCodeExtractor` 与不可变派生方法 `withErrorCodeExtractor`。默认继续使用 `0.1.0` 的 `code` 提取和声明式 API；项目可独立提取额外错误码，真实 HTTP 状态仍保留在 `error.status`。空返回值清空错误码，提取器配置失败保留响应诊断并停止认证恢复与重试。

本次不升级运行时依赖，不修改三字段 `ApiEnvelope<T>`，不定义业务错误码，也不增加会话刷新或通知机制。使用方式及兼容细节见[错误码提取](../sdk/frontend/README.md#可配置错误码提取)。已于 2026-09-13 将 [`@stellarmesh/sdk@0.2.0`](https://www.npmjs.com/package/@stellarmesh/sdk/v/0.2.0) 发布至官方 npm registry，公开访问，`latest` 指向 `0.2.0`。

2026-09-13 已验证并推送源码 commit `65ff52dddabc59d8c704603417af09fdce2337da`。本地 `make verify`、129 个前端测试、Chromium 实际 HTTP 与隔离 tarball 消费全部通过；`release:prepare -- sdk/frontend/v0.2.0` 生成并验证唯一制品 `stellarmesh-sdk-0.2.0.tgz`，摘要如下：

```text
SHA-512 integrity: sha512-k1bs5J6fqVQ2ITuqZl0nuWIWRwsHBbnsqsM6WqQ6FURlzZb6U/PdA64OHFsUxChwadd1lO56FWTnmpxHq29UKA==
SHA-256: a9528fdfadae60d30f85c16eb902d19759571cb2cf7943a6109ae9098e691059
```

对应源码的 [GitHub CI](https://github.com/L1ndenbaum/stellarmesh-sdk/actions/runs/34704294630) 中，前端 HTTP SDK、Go 模块、Python Logging、Python Storage 和 Shell 检查均通过。Storage 集成拉取 `minio/minio@sha256:a1ea29fa28355559ef137d71fc570e508a214ec84ff8083e39bc5428980b015e` 时返回 `pull access denied`，导致汇总检查失败。该失败位于既有 Storage 集成环境，不属于前端包的运行或构建依赖。

本次发布已获得对上述既有 MinIO 镜像拉取失败的明确豁免，豁免仅适用于本次前端 npm 发布，不表示整条 CI 成功。上传使用准备阶段的同一份 tarball，未重新构建或替换制品。

发布后已核对官方 registry 的版本、`latest` 与 SHA-512 integrity；匿名下载的公开 tarball 与本地 SHA-512／SHA-256 完全一致，并通过包根入口的 ESM、公开 TypeScript 类型和运行时消费验证。随后在全新临时项目中使用空 npm 缓存，按包名安装 `@stellarmesh/sdk@0.2.0`，验证安装摘要、新提取入口、声明式请求、metadata 泛型、同步回调类型限制和默认错误码兼容行为。

验收后创建并推送 annotated 组件 tag [`sdk/frontend/v0.2.0`](https://github.com/L1ndenbaum/stellarmesh-sdk/releases/tag/sdk/frontend/v0.2.0)，指向上述发布源码 commit `65ff52dddabc59d8c704603417af09fdce2337da`。该 tag 与已经发布的 npm 版本保持不可变；后续修订使用新版本。本次未更新 KGraph 依赖、业务 Envelope 或部署。

## 前端 HTTP SDK 首次 npm 发布

前端包为 `@stellarmesh/sdk`，首次发布版本 `0.1.0`，采用 MIT 许可证，许可证位于 `sdk/frontend/LICENSE` 并随 npm 包分发。该许可证针对前端包，不改变其他语言模块的许可声明。已于 2026-09-12 发布到官方 npm registry，公开包为 [`@stellarmesh/sdk@0.1.0`](https://www.npmjs.com/package/@stellarmesh/sdk/v/0.1.0)。包归属 npm 组织 `stellarmesh`，由具备组织发布权限的账号维护。

包只提供 ESM JavaScript 和类型声明，唯一公开入口是包根；发布清单只包含 `dist/`、`package.json`、`README.md` 和 `LICENSE`。运行时依赖仍为锁定版本的 Axios。首次发布不升级依赖，组件 tag 为 `sdk/frontend/v0.1.0`。

认证装配已统一为 `createAuthSession` 与 `withAuth(auth, bindingOptions)`，支持显式恢复策略和 Cookie Session。使用前阅读[认证接口迁移](../sdk/frontend/README.md#未发布初版的认证接口迁移)及项目凭证条件提交示例。

首次发布源码 commit 为 `b89936671c17abd4a7eda30e79236e266025e0fa`，包含大写 `HttpErrorKind.HTTP` 等运行时常量及同名类型。发布上传的是一次构建并验证的 tarball，官方 registry 匿名下载的制品与本地 SHA-512／SHA-256 一致，公开 tarball 的 ESM 和 TypeScript 消费验证通过；随后在全新目录使用空 npm 缓存按包名安装 `@stellarmesh/sdk@0.1.0`，确认大写错误常量、声明式入口与安装摘要一致，当时的 `latest` 指向 `0.1.0`。

```text
SHA-256: 2233314a4a24cfa776ebf9cd0ea94ba5aee4960119722e6a65e44abea7a74c98
```

本地 `make verify`、104 个前端测试、Chromium 与隔离消费验证通过。对应源码的 [GitHub CI](https://github.com/L1ndenbaum/stellarmesh-sdk/actions/runs/34675639098) 中前端任务已通过；Storage 集成因拉取外部 MinIO 镜像被拒绝而失败，不能将本次记录视为整条 CI 成功。该集成不属于前端 npm 包的运行或构建依赖。


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

Logging 本次收窄自动类型展开、改为精确敏感字段匹配、统一分组与容器预算，并删除 Go 的两个 panic 错误类别。它是破坏性变更，迁移步骤见[Go 教程](../sdk/go/logging.md)、[Python 教程](../sdk/python/README.md)和[共享清洗约定](../../contracts/logging/sanitization.md)。

Gateway 修复访问日志复制时丢失 `RateLimitResult` 的问题，保留已执行限流阶段的结果；日志副本的 map 和 Roles 与原始状态隔离。公开接口和鉴权、限流决策不变。

源码持续验证、两个 Go tag 的公共消费者工作流，以及 Python 构建、TestPyPI、正式 PyPI 工作流均已通过。发布后另在全新环境验证公共 Go Proxy/checksum database 消费及正式 PyPI 安装、版本元数据、公开格式化 API 和嵌套脱敏输出。本次没有创建根 tag 或重新发布镜像。

未来发布仍须先推送已验证源码并等待持续验证成功，再确认目标 tag 不存在、创建组件 tag。Python 使用同一份构建 artifact 依次发布 TestPyPI 与正式 PyPI；发布后验证真实制品，再更新已发布矩阵。

## 轻量Logging `0.3.0`发布

Go `sdk/go/logging/v0.3.0`删除Event、远程Client、Emitter、Kafka/DLQ和audit语义，改为零第三方依赖的`slog.Handler`安全装饰器。Python `stellarmesh-logging==0.3.0`删除HTTP、Pydantic、后台线程和远程Handler，改为无运行依赖的标准库单行JSON Formatter。

两个语言包只负责脱敏、有界化、异常与不安全值转换，不统一项目日志Schema、Collector、Topic或ClickHouse表。Gateway Logging Adapter不再发布新版本，主干删除该Module；Gateway Core继续使用标准库`slog`。本次只创建Go与Python组件tag，不创建根tag，也不发布镜像。

## 公共日志运行时退役

SDK 主干已经删除未被业务仓库采用的 `logging-service`、ClickHouse sink、迁移镜像及其发布目标。GHCR 中已经发布的 `0.2.0` 镜像、Logging v1/v2 契约和 Go/Python `0.2.0` 包保持不可变，供仍运行旧链路的 KGraph 完成后续迁移；删除主干源码不等于删除历史制品。

后续新项目默认使用标准库结构化 stdout，并由项目自己的 Vector 等 Collector负责解析、持久缓冲、重放和数据库投影。日志表、保留策略和 ClickHouse migration 不再由公共 SDK统一规定。根镜像发布工作流从此只构建 `storage-service`，`v0.2.0` 是三个 Logging 运行时镜像的最后版本。

## Logging v2 `0.2.0` 发布

Logging v2 首次把事件用途与严重程度拆开：`kind` 只允许 `LOG`、`AUDIT`，`level` 只允许 `DEBUG`、`INFO`、`WARNING`、`ERROR`。运行时代码只支持 v2；`contracts/logging/v1` 保留为只读历史契约，不提供 HTTP、Kafka、spool 或 decoder 兼容。

本次发布的制品为：

- Go Logging `sdk/go/logging/v0.2.0`；
- Gateway Logging Adapter `sdk/go/gateway/loggingadapter/v0.2.0`；
- Python Logging `sdk/python/logging/v0.2.0`；
- 根镜像 tag `v0.2.0`，统一生成 logging-service、storage-service、ClickHouse sink 和 migrate 四个镜像。

发布顺序要求先发布 Go Logging，等待公共代理收录；再在 Adapter、logging-service 和 sink 中记录真实 checksum，提交并重新通过持续验证；随后发布 Adapter 与 Python 包，最后创建根镜像 tag。任何已经推送的 tag 都不能移动、删除或复用。

业务环境迁移必须在维护窗口停止 v1 producers，排空客户端、service 队列、v1 Kafka lag 和 v1 spool，备份并执行 ClickHouse `000002`，再按 v2 sink、service、producers 的顺序切换。降级也必须先排空 v2 并整体恢复，不能只回滚单个组件。`AUDIT` 绕过客户端最低级别并优先回放，但仍可能因共享 spool 满、队列满或远程失败而丢弃，不代表合规级不可丢失审计。

## 发布边界

同一个 Git commit 可以产生多类制品，但每类制品使用独立 tag，避免无关组件的版本互相阻断：

- 根 tag `vX.Y.Z` 只触发四个容器镜像构建并推送到 GHCR；
- 父 Go Module tag `sdk/go/vX.Y.Z` 触发 HTTP、Object Storage 等父 SDK能力的公共 Go proxy 消费验证；
- Gateway Go Module tag `sdk/go/gateway/vX.Y.Z` 触发声明式 Gateway、JWT 和 Redis 限流组件的公共消费验证；
- Logging Go Module tag `sdk/go/logging/vX.Y.Z` 触发独立日志契约与客户端的公共消费验证；
- Kafka Go Module tag `sdk/go/mq/kafka/vX.Y.Z` 触发 Kafka公共消费验证；
- Python 日志组件 tag `sdk/python/logging/vX.Y.Z` 只构建并发布 `stellarmesh-logging`；
- Python Storage 组件 tag `sdk/python/storage/vX.Y.Z` 只构建并发布 `stellarmesh-storage`；
- `contracts/logging/v1/` 与 `contracts/storage/v1/` 随源码版本发布，SDK、服务、sink 和迁移制品必须来自经过完整验证的 commit。

不同组件可以有不同版本。例如四个镜像可以保持 `0.1.1`，父 Go SDK 保持 `0.4.0`，Gateway 使用 `0.2.0`，Logging 与 Kafka Module 使用 `0.1.0`，Python 日志包保持 `0.1.2`，`stellarmesh-storage` 继续保持 `0.1.0`。发布前必须先让 `main` 或 `dev` 分支的持续验证通过，不得用 tag 绕过格式、静态检查、测试、镜像构建或集成测试。已经推送的 tag 不得移动、删除、覆盖或强推；制品内容需要修改时必须提升 patch 版本。

当前本地 `dev` 源码已把旧父 SDK `storagecontract` package 内部化为 `services/storage/internal/storagev1`，但尚未发布新的父 SDK 或镜像。公开 `sdk/go/v0.4.0` 仍不可变地包含旧 package；在后续父 SDK 发版前必须将这一删除作为破坏性 Module 边界变化审查，并同步验证 `storage-service` 镜像。Storage v1 的语言无关契约仍位于 `contracts/storage/v1`，不随 Go package 的移动而复制或改名。

## 镜像发布

推送形如 `v0.1.1` 的根 tag 后，`.github/workflows/release-images.yml` 会发布：

- `ghcr.io/l1ndenbaum/stellarmesh-sdk/logging-service`；
- `ghcr.io/l1ndenbaum/stellarmesh-sdk/storage-service`；
- `ghcr.io/l1ndenbaum/stellarmesh-sdk/logging-clickhouse-sink`；
- `ghcr.io/l1ndenbaum/stellarmesh-sdk/logging-clickhouse-migrate`。

工作流同时生成完整版本、`major.minor`、提交 SHA tag、构建来源证明和 SBOM，并发布 `linux/amd64`、`linux/arm64` manifest。上述 GHCR package 为公开制品，开发机、CI 和生产服务器可以匿名拉取，不需要在 Compose 或镜像中保存 GitHub 凭据。若未来重新设为私有，必须改用仅有 `read:packages` 的独立只读身份。

业务仓库可以用完整版本 tag 完成测试，但生产清单必须把验证后的镜像解析为 digest，并以 `image@sha256:...` 形式交给 `server-infrastructure`。四个镜像的 digest 应记录在同一份发布清单中，迁移镜像仍只作为一次性任务运行。

## Go Module 发布

父 SDK、Gateway、Logging 和 Kafka 是四个独立 Module，都不能通过根 tag 发布。确认发布 commit 后分别创建与 Module 目录匹配的 tag：

```sh
git tag -a sdk/go/mq/kafka/v0.1.0 -m '发布 Kafka Go SDK v0.1.0'
git push origin sdk/go/mq/kafka/v0.1.0

git tag -a sdk/go/logging/v0.1.0 -m '发布 Logging Go SDK v0.1.0'
git push origin sdk/go/logging/v0.1.0

git tag -a sdk/go/gateway/v0.2.0 -m '发布 Gateway Go SDK v0.2.0'
git push origin sdk/go/gateway/v0.2.0

git tag -a sdk/go/v0.4.0 -m '发布 Go SDK v0.4.0'
git push origin sdk/go/v0.4.0
```

`.github/workflows/release-go.yml` 只接受上述四个硬编码 tag 前缀。工作流检出 tag中的受版本控制 consumer fixture，再调用 `tests/go-module-consumer.sh public`；脚本会清除私有 Module 配置，在限定时间内等待 `proxy.golang.org` 收录，并使用官方 checksum database从全新的外部 Module构建对应 fixture。Workflow 不内嵌 Go 源码，也不执行公共消费者依赖图断言。

仓库持续验证会把父 SDK、Gateway、Logging 与 Kafka Module移到物理隔离的临时目录，分别以 `GOWORK=off go test ./...` 测试，并复用同一套 fixture编译 Gateway、Logging、父 SDK、Kafka 与组合消费者。Logging-only依赖图必须保持零第三方依赖；Gateway 不得反向依赖父 SDK、AWS、Chi 或 Kafka。依赖边界检查属于持续集成，不放入发布 workflow。

只需要 Gateway 的业务项目固定引入：

```sh
GOWORK=off go get github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/gateway@v0.2.0
go mod tidy
```

只需要 Logging 的业务项目固定引入：

```sh
GOWORK=off go get github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging@v0.1.0
go mod tidy
```

只需要 Kafka 的业务项目固定引入：

```sh
GOWORK=off go get github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/mq/kafka@v0.1.0
go mod tidy
```

同时使用父 SDK、Gateway、Logging 与 Kafka 的项目必须原子升级：

```sh
GOWORK=off go get \
  github.com/L1ndenbaum/stellarmesh-sdk/sdk/go@v0.4.0 \
  github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/gateway@v0.2.0 \
  github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging@v0.1.0 \
  github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/mq/kafka@v0.1.0
go mod tidy
```

`sdk/go/v0.3.0` 仍不可变地包含旧 `gateway` package，不能与独立 Gateway Module同时使用；`sdk/go/v0.2.0` 仍包含旧 `logging` package；`sdk/go/v0.1.1` 仍包含旧 `mq/kafka` package。错误组合可能产生 `ambiguous import`，正确迁移方式是把父 SDK升级到 `v0.4.0`。公共消费验证不得添加本地 `replace`，并应确保 `GOPRIVATE`、`GONOPROXY` 和 `GONOSUMDB` 没有把公开 Module排除在公共代理或校验数据库以外。

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

## Logging `0.1.0` 与父 SDK `0.3.0` 发布顺序

Logging Module从父 `sdk/go` 拆出后，父 SDK使用 `0.3.0` 表达新的 Module边界，Logging Module首次发布为 `0.1.0`。Python `stellarmesh-logging` 保持 `0.1.2`，四个镜像保持 `0.1.1`；本次不创建根 tag或 Python组件 tag。

父 SDK的 Gateway访问日志适配器依赖 Logging Module，因此发布分两个经过验证的 commit进行：

1. 完成 Module、测试、workflow和文档改造，推送 `dev` 并等待持续验证成功；
2. 确认远端不存在 `sdk/go/logging/v0.1.0`，创建并推送该 tag；
3. 等待公共 Proxy、checksum database和 Logging public smoke成功；
4. 在父 `sdk/go` 中以 `GOWORK=off go mod tidy` 记录真实 Logging checksum，提交并再次等待持续验证；
5. 确认远端不存在 `sdk/go/v0.3.0`，创建并推送父 SDK tag；
6. 等待父 SDK public smoke成功；
7. 更新三个内部服务 Module的父 SDK与 Logging checksum，再次完成全仓验证；
8. 在无 `replace` 的全新 Module中分别消费 Logging、父 SDK和 Kafka，并验证组合消费者没有 `ambiguous import`。

如果必须修改 Logging Module源码或发布 workflow，Logging版本提升到 `v0.1.1`；如果必须修改父 SDK内容，父版本提升到 `v0.3.1`。临时网络或 runner故障可以重跑同一不可变 commit的 workflow，但任何已经推送的 tag都不能移动、删除或复用。

## Gateway `0.1.0` 与父 SDK `0.4.0` 发布顺序

Gateway Module从父 `sdk/go` 拆出后，父 SDK使用 `0.4.0` 表达新的 Module边界，Gateway Module首次发布为 `0.1.0`。Logging 与 Kafka继续保持 `0.1.0`，四个镜像保持 `0.1.1`，Python制品版本不变。

发布按以下顺序执行：

1. 完成 Module、测试、workflow和文档改造，推送 `dev` 并等待持续验证成功；
2. 确认远端不存在 `sdk/go/gateway/v0.1.0`，创建并推送该 tag；
3. 等待公共 Proxy、checksum database和 Gateway public smoke成功；
4. 确认远端不存在 `sdk/go/v0.4.0`，在同一已验证 commit创建并推送父 SDK tag；
5. 等待父 SDK public smoke成功；
6. 更新三个内部服务 Module的父 SDK真实 checksum，再次完成全仓验证并推送；
7. 在无 `replace` 的全新 Module中分别消费 Gateway与父 SDK，并与 Logging、Kafka完成组合验证。

如果必须修改 Gateway Module源码或发布 workflow，Gateway版本提升到 `v0.1.1`；如果必须修改父 SDK内容，父版本提升到 `v0.4.1`。临时网络或 runner故障可以重跑同一不可变 commit的 workflow，但任何已经推送的 tag都不能移动、删除或复用。

## Gateway `0.2.0` 响应协议解耦发布

Gateway `0.2.0` 删除内置的 Stellarmesh `ApiEnvelope`，默认错误和健康成功响应改为协议中立的纯文本，并新增项目级 `HealthResponder`。这是可观察响应格式的破坏性变化，但不改变 Module 边界，因此父 SDK 继续保持 `0.4.0`，其他 Go Module、Python 包和四个镜像也不重新发布。

发布步骤：

1. 完成 Gateway 实现、消费者 fixture 和中文迁移文档，推送 `dev` 并等待持续验证成功；
2. 确认远端不存在 `sdk/go/gateway/v0.2.0`，在已验证 commit 创建并推送带注释的 tag；
3. 等待公共 Proxy、checksum database 和 Gateway 公开消费验证成功；
4. 在无 `replace` 的全新 Module 中消费 Gateway `v0.2.0`，编译默认响应器、项目错误响应器和健康响应器；
5. 不创建父 SDK、根镜像或 Python tag。

依赖 `v0.1.0` 默认 JSON 正文的项目必须先在业务仓库实现 `ErrorResponder` 和 `HealthResponder`，再升级到 `v0.2.0`。如果必须修改已经推送 tag 的源码或发布内容，应提升到 `v0.2.1`；不得移动、删除或复用 `v0.2.0`。

## 未发布的 Gateway 访问日志边界

当前本地 `dev` 源码已把 Gateway Core 的默认访问日志改为标准库 `slog`，并新增独立嵌套 Module `sdk/go/gateway/loggingadapter` 适配 Stellarmesh Logging。Gateway Core 不再依赖 Logging；Adapter 只依赖通用 `gateway.AccessLogger` 和轻量 `logging.Emitter`，不拥有远程客户端、Sink 或持久化语义。

这组改动仍在继续评估，本轮只形成本地提交，不推送远程、不创建 tag、不修改 `release-go.yml`，也不更新“发布后验证”中的已发布版本集合。正式发布前需要重新确定 Gateway 与 Adapter 版本、兼容边界、公共代理 fixture 和业务仓库迁移方式。

## 发布后验证

发布完成后至少确认：

1. 父 Go SDK `0.4.0`、Gateway Module `0.2.0`、Logging Module `0.1.0`、Kafka Module `0.1.0` 已发布；四个镜像保持 `0.1.1`，Python Logging 保持 `0.1.2`，`stellarmesh-storage` 仍为 `0.1.0`；
2. 从 TestPyPI 和正式 PyPI 的全新 Python 3.11 环境安装 `stellarmesh-logging==0.1.2`，验证 import、版本元数据、严格 service 校验与标准 `logging.Handler`；
3. 从公共 `GOPROXY` 和 `GOSUMDB` 获取父 `sdk/go/v0.4.0`、Gateway `sdk/go/gateway/v0.2.0`、Logging `sdk/go/logging/v0.1.0` 与 Kafka `sdk/go/mq/kafka/v0.1.0`，不使用 `replace`，分别完成独立和组合消费测试；
4. 使用空临时 `DOCKER_CONFIG` 匿名拉取四个 `0.1.1` 镜像，确认双架构 manifest、provenance 和 SBOM，并记录不可变 digest；
5. 使用临时合法认证文件启动 `logging-service:0.1.1`，在 Kafka 不可用但 spool 可写时确认服务可以降级接收；
6. 业务环境已预先创建源 Topic、DLQ Topic、ClickHouse database、对象存储 Bucket 和最小权限身份；
7. 先运行迁移制品，再启动 sink 和 ingester；
8. 日志服务与 `storage-service` 的 `/health/ready`、`/metrics` 均符合预期；
9. 有效事件落库、无效消息进入 DLQ、预签名上传下载和 namespace 授权均通过最小端到端验证；
10. 回滚时不自动执行 down migration，先按发布清单和数据备份策略处理。
