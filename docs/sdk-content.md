# SDK 内容

## 目标与边界

`stellarmesh-sdk` 把原先散落在不同项目中的基础 HTTP、网关、日志和对象存储能力集中维护。各项目可以引用相同的 SDK，并独立部署同一版本的服务制品，使用各自的业务路由、Kafka Topic、ClickHouse database、对象存储 namespace、账号与凭据，不需要复制公共源码。

仓库有意不包含以下内容：

- Docker Compose 与项目网络拓扑；
- `.env` 或任何生产地址、账号和 Secret；
- ClickHouse database、用户、角色和配额的创建逻辑；
- Kafka Topic、principal 和 ACL 的创建逻辑；
- S3 或 MinIO Bucket、Policy、CORS、Lifecycle、Versioning 和 ACL 的管理逻辑；
- 多项目中央对象存储凭据池；
- 在常驻服务启动阶段自动执行迁移的入口。

这些内容属于业务项目的开发部署配置或 `server-infrastructure` 的生产资源与发布清单。

## 目录与制品

| 路径 | 内容 | 发布形式 |
| --- | --- | --- |
| `contracts/logging/v1/` | 日志事件、DLQ v1/v2、尺寸限制、OpenAPI 和共享测试数据 | 随仓库版本发布 |
| `contracts/storage/v1/` | 对象存储控制面 OpenAPI、访问配置 Schema、统一限制和共享测试数据 | 随仓库版本发布 |
| `sdk/go/` | Go 公共 HTTP、环境配置、Storage 契约和对象存储客户端 | Go module |
| `sdk/go/gateway/` | 声明式 Gateway、JWT 认证、Redis 限流和旁路观测 | 独立 Go module |
| `sdk/go/gateway/loggingadapter/` | Gateway 到 Stellarmesh Logging 的可选访问日志适配器 | 开发中，尚未发布 |
| `sdk/go/logging/` | Logging v1 模型、校验、异步客户端和 `slog.Handler` | 独立 Go module |
| `sdk/go/mq/kafka/` | Kafka 连接、Publisher、Topic 检查与 TLS/SASL 配置 | 独立 Go module |
| `sdk/python/` | Python 日志客户端、类型模型、标准 Handler 与日志门面 | `stellarmesh-logging` Python package |
| `sdk/python/storage/` | Python 对象存储同步与异步客户端 | `stellarmesh-storage` Python package |
| `services/logging/` | HTTP 接收、内存队列、控制台输出、Kafka 发布与失败暂存 | 常驻服务镜像 |
| `services/storage/` | 项目级对象存储认证、授权、readiness 与预签名控制面 | 常驻服务镜像 |
| `sinks/clickhouse/` | Kafka 消费、批量写入和 offset 提交 | 常驻 sink 镜像 |
| `sinks/clickhouse/migrations/` | `log_events` 表的版本化 up/down SQL | 一次性迁移镜像 |

建议发布四个相互独立、但来自同一 Git commit 的镜像：

- `ghcr.io/l1ndenbaum/stellarmesh-sdk/logging-service`；
- `ghcr.io/l1ndenbaum/stellarmesh-sdk/storage-service`；
- `ghcr.io/l1ndenbaum/stellarmesh-sdk/logging-clickhouse-sink`；
- `ghcr.io/l1ndenbaum/stellarmesh-sdk/logging-clickhouse-migrate`。

这些镜像是公开制品，可以匿名拉取；测试环境固定完整版本，生产环境固定已验证的 manifest digest。

迁移镜像与服务镜像分开，目的不是形成新的安全边界，而是让外部编排器能够用迁移身份一次性运行固定 digest 的迁移，并确保迁移凭据不会进入常驻容器。

## 日志协议 v1

所有语言和服务共享同一个 `LogEvent`：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `event_id` | UUID 字符串 | 事件唯一标识；客户端未提供时生成 |
| `timestamp` | RFC 3339 时间 | 统一转换为 UTC |
| `level` | 枚举 | `DEBUG`、`INFO`、`WARNING`、`ERROR`、`AUDIT` |
| `service` | 非空且已去除首尾空白的字符串 | 业务服务稳定标识 |
| `message` | 非空字符串 | 日志消息 |
| `trace_id` | 字符串 | 可由业务显式提供或通过 provider 注入 |
| `metadata` | JSON 对象 | 扩展字段；SDK 会转换不安全或不可序列化的值 |

wire payload 必须显式包含以上全部字段。`timestamp` 使用规范 RFC 3339 形式，UTC 后缀必须写为大写 `Z`，JSON 中不接受 `NaN` 或无穷值；`level` 只接受规范的大写值；`service` 必须非空且已经去除首尾空白，避免认证身份存在多个视觉等价写法；`message` 只要求去除空白后非空，允许保留首尾空白和换行。SDK 的调用方构造器仍可填充默认字段并把 `WARN` 归一化为 `WARNING`，严格 wire decoder 不接受省略字段或非规范枚举。HTTP 单条与批量入口、Go/Python decoder 和 sink 共用相同的严格事件约束。

HTTP 接口为 `/v1/log-events` 和 `/v1/log-events/batch`，鉴权头为 `X-Logging-Service-Token`。Kafka 协议的默认 Topic 是 `stellarmesh.logging.events.v1`。这是规范默认值，不表示运行时可以自动创建 Topic。单条规范事件的紧凑 JSON 最大为 `900KiB`，HTTP body 与 Kafka 完整消息最大为 `1MiB`，应用层 Kafka key/value 预算为 `960KiB`，剩余空间保留给协议封装。Kafka 分区键在存在 `trace_id` 时使用其 SHA-256 摘要，否则使用 `event_id`；这样既保留同一 trace 的稳定分区，也不会因超长 trace 重复占用 key 而让已确认事件无法发布。

HTTP `202 Accepted` 表示事件已经由 Kafka 全同步副本确认，或已经原子提交到接收服务的持久 spool；它不表示 ClickHouse 已写入。队列已满返回 `503`，请求体、单条事件或批量事件数超限返回 `413`，请求或事件无效返回 `400`，令牌无效返回 `401`，token 与事件 `service` 不匹配返回 `403`。

## Go SDK

父 Module `sdk/go` 包含以下可复用包：

- `http/api`：统一响应 envelope、JSON 解码、路由和中间件；
- `http/headers`：标准请求头读写；
- `http/server`：带超时的 HTTP server 构造；
- `objectstorage`：namespace 绑定的 provider-neutral 小接口、对象模型、参数校验和稳定错误；
- `objectstorage/s3store`：基于 AWS SDK for Go v2 的 AWS S3 与 S3-compatible 适配器；
- `storagecontract`：Storage v1 的统一限制、严格访问配置和 capability 校验；
- `envconfig`：不依赖业务 settings 的基础环境变量解析，并提供显式错误的严格 loader。

`sdk/go/gateway` 是独立 Gateway Module，包含固定安全顺序的声明式网关、静态路由、可信代理、CORS、反向代理、健康检查、旁路观测、HS256 JWT 认证和 Redis 原子令牌桶。当前本地 `dev` 源码只直接依赖 JWT 和 Redis，不依赖父 SDK、Logging、AWS SDK、对象存储、Chi 或 Kafka。Gateway 默认把通用访问记录交给标准库 `slog`，项目可以关闭默认日志或注入自己的 `AccessLogger`。完整接入方式见[Go 网关 SDK](sdk/go/gateway.md)。

`sdk/go/gateway/loggingadapter` 是开发中的独立嵌套 Module，只依赖 Gateway 和轻量 Logging Module。它把 `gateway.AccessLog` 转成 Stellarmesh Logging Event，但不创建远程客户端、不拥有 Emitter 生命周期，也不定义 logging-service、Kafka、spool、ClickHouse 或 Sink 行为。本 Module 尚未发布，不能作为正式项目依赖引用。

`sdk/go/logging` 是只依赖 Go 标准库的独立轻量 Module，提供 Logging v1 协议模型、严格校验、元数据清洗、异步批量 HTTP 客户端、标准 `slog.Handler` 和兼容日志门面。只使用日志能力的项目不需要引入父 SDK及其 Gateway、AWS 或 Redis 依赖。完整接入方式见 [Go Logging SDK](sdk/go/logging.md)。

`sdk/go/mq/kafka` 是独立的轻量 Module，提供显式 Topic、并行 Topic 检查、Hash 分区且要求全副本确认的 Publisher，以及 `PLAINTEXT`、TLS、mTLS、SASL/PLAIN、SCRAM-SHA-256 和 SCRAM-SHA-512 配置。Consumer 继续由业务项目通过 `Connection.Dialer()` 构造 `kafka-go.Reader`，自行拥有 consumer group、offset、提交和重试语义。完整接入方式见 [Go Kafka SDK](sdk/go/kafka.md)。

网关项目使用 `gateway.New(options ...Option)` 构造一个普通 `http.Handler`。`WithXxx` 只声明使用哪些组件，不改变执行顺序。路由解析、客户端地址解析、鉴权、授权、限流、转发策略和 upstream 解析发生错误时停止转发；访问日志与 Observer 失败只产生旁路观测，不改变已经完成的 HTTP 响应。静态路由默认需要认证，公开路由必须显式设置 `AccessPublic`，未匹配路径返回 `404`。完整接入方式见[Go 网关 SDK](sdk/go/gateway.md)。

日志客户端使用同时受事件数和规范化 JSON 字节数限制的内存队列，调用 `Emit`、`Enqueue`、`slog.Handler` 或日志级别方法时不会等待网络。构造函数会立即校验 URL、token、service、容量和重试限制。客户端后台对网络异常及明确的临时 HTTP 状态执行最多三次带抖动指数退避，支持有上限的 `Retry-After`，并复用原 `event_id`；队列满、事件无效、客户端关闭、重试耗尽或响应不符合契约时调用 `OnDrop`，callback 的 panic 会被隔离并限频写到 stderr。`Close` 到期会取消在途 HTTP 和退避等待，并逐条报告尚未发送的事件。SDK 没有落盘队列，因此在收到合法 `202` 以前只提供 best-effort 投递；进程退出前应调用 `Close` 并给出明确超时。

## Python SDK

`stellarmesh_logging` 提供：

- Pydantic `LogEvent`、批量请求与响应模型；
- Pydantic `DeadLetter` 与标准事件、DLQ Topic 常量；
- `ClientConfig` 和有界队列 `Client`；
- 标准库适配器 `StellarmeshHandler`；
- `Logger`、`get_logger`、`set_default_client`；
- 同步和异步关闭入口；
- trace provider、drop handler、日志级别过滤和元数据清洗；
- 协议编码与解码函数及 `py.typed` 类型声明。

Python 客户端使用后台线程发送批量 HTTP 请求，不依赖任一业务项目的配置模块、Web 框架或请求上下文。`StellarmeshHandler` 只把标准 `LogRecord` 转成规范事件，不创建控制台输出、额外队列或重试线程；远程最低级别仍由 `ClientConfig.minimum_level` 统一过滤。业务项目通过构造参数或 provider 注入服务名、令牌和 trace id。provider 与 drop handler 的异常不会传播到业务调用方；worker 具有明确的失败状态，队列的事件数和累计字节均有上限，并提供 best-effort 进程退出排空兜底。

独立发布的 `stellarmesh-storage` 包提供严格 Pydantic 模型、同步 `Client` 和异步 `AsyncClient`。它只向项目级 `storage-service` 发送控制面请求，通过预签名 URL 让对象字节直接在客户端与 S3 或 MinIO 之间传输。service token 不会进入数据面请求；单次上传超过 5 GiB 时明确要求调用方管理 Multipart，不在客户端隐藏 UploadID 和 Part 状态；文件下载使用同目录临时文件并在成功后原子替换目标。完整用法见 [Python 对象存储 SDK](sdk/python/storage.md)。

## 对象存储协议与数据链路

Storage v1 的业务请求只包含逻辑 `namespace` 和 `key`，不接受 Bucket。每个项目独立部署一份 `storage-service`，由只读访问文件把 namespace 映射到 Bucket 与 Prefix，并把 principal token 映射到 `read`、`write`、`delete` capability。同一实例可以声明多个 namespace，但所有 namespace 必须使用同一项目的 AWS IAM Role、Web Identity 或 MinIO 项目凭据。

```text
Go 服务 -> 进程内 objectstorage SDK -> S3 或 MinIO

Python 或其他客户端
  -> storage-service：认证、授权、readiness、预签名
  -> S3 或 MinIO：使用返回的 Method 与 Signed Headers 直传对象字节
```

`storage-service` 不代理对象内容、不写临时文件，也不创建 Bucket。readiness 初始为 false，只有全部 namespace 的只读可访问性检查成功才变为 ready；检查失败时受保护路由 fail-close 返回 `503`。未知 token 返回 `401`，已认证但没有 namespace 或 capability 权限返回 `403`。预签名 URL、Bucket、完整 Key 和 principal 不进入指标标签或 Observer。详细部署参数和责任边界见 [storage-service 部署文档](storage-service.md)，Go 进程内用法见 [Go 对象存储 SDK](sdk/go/object-storage.md)。

## 日志数据链路

```text
业务进程
  -> Go 或 Python SDK 有界队列
  -> logging-service HTTP 内存队列
  -> Kafka topic
  -> clickhouse sink
       -> 有效事件 -> ClickHouse log_events
       -> 无效消息 -> Kafka DLQ topic
```

`logging-service` 从挂载的受保护 JSON 文件加载 service-token 绑定关系；token 只以 SHA-256 digest 留在进程内，比较使用常量时间操作。同一 service 可以同时配置新旧 token 完成滚动轮换，事件不能伪造其他 service 身份。服务启动时还会使用与运行期相同的 Kafka TLS/SASL transport 检查 Topic 可访问性；检查失败但本地 spool 可用时进入降级接收，而不是因为 Kafka 暂时不可用直接退出。

正常批次先由 Kafka 全同步副本确认；Kafka 发布失败时，批次先写入有总容量上限的本地分段 spool。只有其中一条持久化路径成功后，事件副本才进入有事件数和字节数双重上限的异步控制台队列。控制台按事件输出紧凑单行 JSON，writer 阻塞、编码失败或队列满只丢弃控制台副本，不影响 Kafka、spool 或已经返回的 HTTP 结果。

spool 后台按段定期重放。`ERROR` 和 `AUDIT` 进入高优先级分段，其他级别进入普通分段；每轮先尝试高优先级，但其失败不会阻止普通分段获得尝试。本实现没有为 priority 预留独立容量，因此普通日志已经占满共同 spool 时，新的 `ERROR` 或 `AUDIT` 仍可能无法落盘；容量告警和生产日志级别过滤仍是必要的保护。一个接收批次先完整写入 `.staging/` 并执行 `fsync`，再通过目录重命名一次提交到 `batches/`，因此不会暴露只包含部分优先级或部分分段的批次。升级时仍会读取旧版本 `regular/` 和 `priority/` 中的 `.ready.jsonl`；这项兼容不包括业务项目自行实现的其他 JSONL 格式。损坏或不兼容的整个 segment 会连同原因元数据移入持久 `quarantine/`；publisher 判定为永久失败时，回放会把失败范围缩小到单条，只隔离不可发布记录并继续发布同段正常记录。容量账本会为每个 live segment 预留等同自身大小的隔离副本空间和 `64KiB` 元数据空间，使原文件、隔离记录和元数据并存的瞬时状态仍受同一硬上限约束；配置容量因此大于业务事件的净可用容量。隔离数据计入 spool 容量并等待人工处置，不会自动删除。暂时失败时保留原 segment，因此 Kafka 在中途恢复时允许出现重复事件，不能把这条链路理解为 exactly-once。数据目录必须由业务部署持久化，但目录挂载方式由业务项目管理。

接收队列同时限制尚未获得持久确认的事件数和规范化 JSON 字节数，不按 HTTP 请求数限制；发布批次也同时受事件数和字节数约束。请求进入队列后会等待当前批次由 Kafka 全部同步副本确认，或在 Kafka 失败时由本地 spool 原子提交；只有满足其中一项才返回 `202`，两者均失败则返回 `503`。客户端请求提前取消不会撤销已经入队的事件，客户端重试可能产生重复。队列已满、服务关闭或持久路径均不可用时，服务会通过 `503` 或 readiness 暴露背压。`/health/live` 只判断进程存活，`/health/ready` 表示服务最近一次确认仍能可靠转交或缓冲新事件；后台 Kafka 检查并行探测去重后的 broker，任一 broker 能访问目标 Topic 即成功，并可在空 spool 时恢复就绪状态。后续请求也会重新探测持久路径。`/metrics` 暴露有界标签的 Prometheus 指标，包括控制台副本的 `emitted`、`dropped`、`failed` 结果；`/health` 保留为存活检查兼容入口。

ClickHouse sink 使用显式 consumer group，并要求独立 DLQ Topic。reader 只预取一条，消费批次同时受消息数和 Kafka key/value 总字节数约束；这些是 payload 预算而不是进程 RSS 硬上限，协议缓冲、解析和编码仍会产生临时分配。每批消息先严格解析：普通无效事件按 `dead-letter.schema.json` 编码，保留原 Topic、partition、offset、时间、key 和 Base64 原始载荷；超过源消息上限且不适合复制原始载荷的消息按 `dead-letter-v2.schema.json` 编码，只保留源坐标、长度和 SHA-256 摘要。只有 ClickHouse 插入、DLQ 发布和整批 offset 提交依次成功后，该批次才完成；任一步失败都保留整批重试。这样坏消息不会永久阻塞分区，但 ClickHouse 行和 DLQ 记录都可能因提交失败而重复，消费者必须按 at-least-once 处理。

sink 启动时检查源 Topic、DLQ Topic 和 ClickHouse 运行时凭据。独立观测端口提供 `/health/live`、`/health/ready` 和 `/metrics`；Kafka 拉取、ClickHouse 插入、DLQ 发布或 offset 提交失败时 readiness 下降，成功恢复后回升。关闭时使用独立的排空超时处理内存中的最后一批，不复用已经取消的进程 context。

## ClickHouse Schema 与迁移

表名固定为 `log_events`，包含 `event_id`、`timestamp`、`level`、`service`、`message`、`trace_id`、`metadata_json` 和 `ingested_at` 八列，使用 `ReplacingMergeTree(ingested_at)`，按月份分区并以 `event_id` 排序。

本仓库拥有表、字段、引擎和后续 Schema 演进；`server-infrastructure` 拥有 ClickHouse database、迁移身份、运行时身份和授权。迁移镜像可以从空 database 执行，也可以由外部编排器指定 revision。常驻接收服务与 sink 不包含迁移命令，也不应获得 DDL 权限。

生产发布顺序应为：

```text
resources plan/apply
  -> preflight
  -> backup
  -> migrate
  -> verify
  -> deploy
  -> postcheck
```

生产清单最终固定四个镜像的 digest。迁移失败必须阻止日志常驻服务发布；不应通过自动 downgrade 掩盖失败。`storage-service` 不参与 ClickHouse 迁移，但仍应与 SDK 和 Storage v1 契约使用同一验证版本。

## 版本兼容规则

- 协议目录的 `v1` 是消息兼容边界；新增可选字段必须同时更新契约与四方测试。
- Storage v1 的 OpenAPI、访问配置 Schema、Go 服务和 Python 客户端必须同步更新共享限制与 testdata。
- DLQ 记录是独立的 v1 协议，不得直接投回正常事件 Topic；修复并重放前必须显式解码 `payload_base64`、校验来源坐标并经过审计。
- 删除字段、改变含义或收紧校验属于破坏性变化，应创建新的协议版本与 Topic。
- Go SDK、两个 Python 包、接收服务、storage-service、sink 与迁移镜像应使用同一仓库 commit 构建。
- 每个业务项目可以选择自己的升级窗口，但不能混用未验证的协议版本。
