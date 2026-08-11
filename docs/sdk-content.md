# SDK 内容

## 目标与边界

`stellarmesh-sdk` 把原先散落在不同项目中的日志客户端、日志接收服务和 ClickHouse 落盘逻辑集中维护。各项目可以独立部署同一版本的服务制品，使用各自隔离的 Kafka Topic、ClickHouse database、账号与凭据，不需要复制服务源码。

仓库有意不包含以下内容：

- Docker Compose 与项目网络拓扑；
- `.env` 或任何生产地址、账号和 Secret；
- ClickHouse database、用户、角色和配额的创建逻辑；
- Kafka Topic、principal 和 ACL 的创建逻辑；
- 在常驻服务启动阶段自动执行迁移的入口。

这些内容属于业务项目的开发部署配置或 `server-infrastructure` 的生产资源与发布清单。

## 目录与制品

| 路径 | 内容 | 发布形式 |
| --- | --- | --- |
| `contracts/logging/v1/` | 日志事件、DLQ 记录的 JSON Schema、OpenAPI 和共享测试数据 | 随仓库版本发布 |
| `sdk/go/` | Go 公共 HTTP、Kafka、环境配置与日志客户端 | Go module |
| `sdk/python/` | Python 日志客户端、类型模型与日志门面 | Python package |
| `services/logging/` | HTTP 接收、内存队列、控制台输出、Kafka 发布与失败暂存 | 常驻服务镜像 |
| `sinks/clickhouse/` | Kafka 消费、批量写入和 offset 提交 | 常驻 sink 镜像 |
| `sinks/clickhouse/migrations/` | `log_events` 表的版本化 up/down SQL | 一次性迁移镜像 |

建议发布三个相互独立、但来自同一 Git commit 的镜像：

- `stellarmesh-logging-service`；
- `stellarmesh-logging-clickhouse-sink`；
- `stellarmesh-logging-clickhouse-migrate`。

迁移镜像与服务镜像分开，目的不是形成新的安全边界，而是让外部编排器能够用迁移身份一次性运行固定 digest 的迁移，并确保迁移凭据不会进入常驻容器。

## 日志协议 v1

所有语言和服务共享同一个 `LogEvent`：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `event_id` | UUID 字符串 | 事件唯一标识；客户端未提供时生成 |
| `timestamp` | RFC 3339 时间 | 统一转换为 UTC |
| `level` | 枚举 | `DEBUG`、`INFO`、`WARNING`、`ERROR`、`AUDIT` |
| `service` | 非空字符串 | 业务服务稳定标识 |
| `message` | 非空字符串 | 日志消息 |
| `trace_id` | 字符串 | 可由业务显式提供或通过 provider 注入 |
| `metadata` | JSON 对象 | 扩展字段；SDK 会转换不安全或不可序列化的值 |

HTTP 接口为 `/v1/log-events` 和 `/v1/log-events/batch`，鉴权头为 `X-Logging-Service-Token`。Kafka 协议的默认 Topic 是 `stellarmesh.logging.events.v1`。这是规范默认值，不表示运行时可以自动创建 Topic。

HTTP `202 Accepted` 只表示事件已经进入接收服务的内存队列，不表示已写入 Kafka 或 ClickHouse。队列已满返回 `503`，批量事件过多返回 `413`，请求或事件无效返回 `400`，令牌无效返回 `401`，token 与事件 `service` 不匹配返回 `403`。

## Go SDK

`sdk/go` 包含以下可复用包：

- `logging`：协议模型、校验、元数据清洗、异步批量 HTTP 客户端和日志门面；
- `http/api`：统一响应 envelope、JSON 解码、路由和中间件；
- `http/headers`：标准请求头读写；
- `http/server`：带超时的 HTTP server 构造；
- `mq/kafka`：具有显式 topic、可复用 Topic 启动检查、TLS、mTLS、SASL/PLAIN 和 SCRAM 配置的 Kafka publisher；
- `envconfig`：不依赖业务 settings 的基础环境变量解析。

日志客户端使用有界内存队列，调用 `Emit` 或日志级别方法时不会等待网络。构造函数会立即校验 URL、token、service 和容量限制。队列满、事件无效、客户端关闭、请求失败或响应不符合契约时，客户端返回 `false` 或调用 `OnDrop`；callback 的 panic 会被隔离并限频写到 stderr。客户端不会在业务请求线程中无限重试；进程退出前应调用 `Close` 并给出明确超时。

## Python SDK

`stellarmesh_logging` 提供：

- Pydantic `LogEvent`、批量请求与响应模型；
- Pydantic `DeadLetter` 与标准事件、DLQ Topic 常量；
- `ClientConfig` 和有界队列 `Client`；
- `Logger`、`get_logger`、`set_default_client`；
- 同步和异步关闭入口；
- trace provider、drop handler、日志级别过滤和元数据清洗；
- 协议编码与解码函数及 `py.typed` 类型声明。

Python 客户端使用后台线程发送批量 HTTP 请求，不依赖任一业务项目的配置模块、Web 框架或请求上下文。业务项目通过构造参数或 provider 注入服务名、令牌和 trace id。provider 与 drop handler 的异常不会传播到业务调用方；worker 具有明确的失败状态，并提供 best-effort 进程退出排空兜底。

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

`logging-service` 从挂载的受保护 JSON 文件加载 service-token 绑定关系；token 只以 SHA-256 digest 留在进程内，比较使用常量时间操作。同一 service 可以同时配置新旧 token 完成滚动轮换，事件不能伪造其他 service 身份。服务启动时还会使用与运行期相同的 Kafka TLS/SASL transport 检查 Topic 可访问性。

正常批次写到控制台并发布 Kafka；Kafka 发布失败时，批次写入有总容量上限的本地分段 spool，后台按段定期重放。`ERROR` 和 `AUDIT` 进入高优先级分段，其他级别进入普通分段，重放时始终先处理高优先级。一个接收批次先完整写入 `.staging/` 并执行 `fsync`，再通过目录重命名一次提交到 `batches/`，因此不会暴露只包含部分优先级或部分分段的批次。升级时仍会读取旧版本 `regular/` 和 `priority/` 中的 `.ready.jsonl`；这项兼容不包括业务项目自行实现的其他 JSONL 格式。只有整段发布成功才删除，因此 Kafka 在中途恢复时允许出现重复事件，不能把这条链路理解为 exactly-once。数据目录必须由业务部署持久化，但目录挂载方式由业务项目管理。

接收队列按尚未开始发布的事件数限制，不按 HTTP 请求数限制。队列已满、服务关闭或 Kafka 失败且 spool 无法继续持久化时，服务会通过 `503` 或 readiness 暴露背压。`/health/live` 只判断进程存活，`/health/ready` 表示服务仍能可靠转交或缓冲新事件，`/metrics` 暴露有界标签的 Prometheus 指标。`/health` 保留为存活检查兼容入口。

ClickHouse sink 使用显式 consumer group，并要求独立 DLQ Topic。每批消息先严格解析：有效事件批量写入 `log_events`，无效事件按 `dead-letter.schema.json` 编码，保留原 Topic、partition、offset、时间、key 和 Base64 原始载荷。只有 ClickHouse 插入、DLQ 发布和整批 offset 提交依次成功后，该批次才完成；任一步失败都保留整批重试。这样坏消息不会永久阻塞分区，但 ClickHouse 行和 DLQ 记录都可能因提交失败而重复，消费者必须按 at-least-once 处理。

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

生产清单最终固定三个镜像的 digest。迁移失败必须阻止常驻服务发布；不应通过自动 downgrade 掩盖失败。

## 版本兼容规则

- 协议目录的 `v1` 是消息兼容边界；新增可选字段必须同时更新契约与四方测试。
- DLQ 记录是独立的 v1 协议，不得直接投回正常事件 Topic；修复并重放前必须显式解码 `payload_base64`、校验来源坐标并经过审计。
- 删除字段、改变含义或收紧校验属于破坏性变化，应创建新的协议版本与 Topic。
- SDK、接收服务、sink 与迁移镜像应使用同一仓库 tag 构建。
- 每个业务项目可以选择自己的升级窗口，但不能混用未验证的协议版本。
