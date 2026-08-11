# 接入 SDK

本文件说明业务项目接入 SDK、日志接收服务、ClickHouse sink 和迁移制品的完整流程。只接入语言客户端时，可直接阅读 [Go SDK 接入教程](sdk/go/README.md) 或 [Python SDK 接入教程](sdk/python/README.md)。

## 接入前准备

业务项目需要自行管理开发环境和项目级部署配置。本仓库不提供 Compose 或 `.env` 模板。接入前应明确以下值：

- 当前项目的日志接收服务地址；
- 项目独立的服务令牌；
- 稳定且可区分的业务 `service` 名称；
- 项目独立的 Kafka Topic、consumer group 和 ACL；
- 项目独立的 Kafka DLQ Topic、保留策略和 sink 生产权限；
- 项目独立的 ClickHouse database、迁移身份与运行时身份；
- 日志接收服务本地 spool 的持久化目录。

开发环境可由业务项目自行用 Compose、测试容器或共享开发基础设施提供。生产资源必须通过 `server-infrastructure` 声明和编排。

## Go 项目

安装固定版本：

```sh
go get github.com/L1ndenbaum/stellarmesh-sdk/sdk/go@v0.1.0
```

在应用启动时构造一个进程级客户端和 logger：

```go
package main

import (
    "context"
    "log"
    "time"

    logging "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging"
)

func main() {
    client, err := logging.NewClient(logging.ClientConfig{
        BaseURL: "http://logging-service:8091",
        Token:   "由业务配置层注入",
        OnDrop: func(event logging.Event, err error) {
            log.Printf("remote log dropped: event_id=%s err=%v", event.EventID, err)
        },
    })
    if err != nil {
        log.Fatal(err)
    }
    logger, err := logging.NewLogger(logging.LoggerConfig{
        Service: "example-api",
        Emitter: client,
        TraceIDProvider: func(ctx context.Context) string {
            return traceIDFromProjectContext(ctx)
        },
    })
    if err != nil {
        log.Fatal(err)
    }

    ctx := context.Background()
    logger.Info(ctx, "order created", "", map[string]any{"order_id": "123"})

    shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
    defer cancel()
    if err := client.Close(shutdownCtx); err != nil {
        log.Printf("logging drain failed: %v", err)
    }
}

func traceIDFromProjectContext(context.Context) string {
    return ""
}
```

注意：

- `Debug`、`Info`、`Warning`、`Error` 和 `Audit` 返回值表示事件是否成功进入 SDK 队列；
- Go 构造函数会立即拒绝非法 URL、空 token、空 service 和负数限制，不把配置错误延迟到后台 worker；
- `true` 不代表事件已持久化；
- `traceID` 参数非空时优先使用显式值，否则调用 `TraceIDProvider`；
- `OnDrop` 只应执行轻量、不会递归调用同一 logger 的降级动作；
- HTTP handler、worker 和定时任务共享同一个客户端即可，不要为每次请求创建后台 worker。

## Python 项目

本地开发可从仓库子目录安装：

```sh
pip install ./sdk/python
```

发布到内部 Python registry 后，应固定包版本：

```sh
pip install stellarmesh-logging==0.1.0
```

在应用生命周期入口显式配置客户端：

```python
from stellarmesh_logging import (
    Client,
    ClientConfig,
    get_logger,
    set_default_client,
    shutdown_logging,
)


def current_trace_id() -> str:
    return ""


client = Client(
    ClientConfig(
        base_url="http://logging-service:8091",
        token="由业务配置层注入",
        service="example-worker",
        trace_id_provider=current_trace_id,
        drop_handler=lambda event, error: print(
            f"remote log dropped: event={event} error={error}"
        ),
    )
)
set_default_client(client)
logger = get_logger(__name__).bind(component="scheduler")

logger.info("job started", job_id="job-123")


async def application_shutdown() -> None:
    await shutdown_logging(timeout=3.0)
```

同步入口使用 `shutdown_logging_sync(timeout=3.0)`。存在 asyncio event loop 时必须 `await shutdown_logging()`，不能调用同步包装器。`debug`、`info`、`warning`、`error`、`audit` 和 `exception` 都返回是否进入本地队列；它们不会等待远端持久化。

`drop_handler` 同样不能再调用当前远端 logger，避免失败时递归。若业务已有 trace 上下文，应在 provider 中适配；SDK 不直接依赖 FastAPI、Django、Flask、OpenTelemetry 或业务自定义 context。

## 部署日志接收服务

业务项目从固定 tag 或 digest 引用 `stellarmesh-logging-service` 镜像，并自行管理网络、端口、持久卷和配置注入。服务配置如下：

| 配置键 | 默认值 | 说明 |
| --- | --- | --- |
| `STELLARMESH_LOGGING_ADDR` | `:8091` | HTTP 监听地址 |
| `STELLARMESH_LOGGING_AUTH_FILE` | 无 | 必填；挂载的 service-token 绑定配置 |
| `STELLARMESH_LOGGING_DATA_DIR` | `/var/lib/stellarmesh-logging` | 本地持久化根目录 |
| `STELLARMESH_LOGGING_CONSOLE_COLOR` | `true` | 控制台颜色 |
| `STELLARMESH_LOGGING_READ_HEADER_TIMEOUT` | `5s` | HTTP header 读取超时 |
| `STELLARMESH_LOGGING_READ_TIMEOUT` | `10s` | HTTP 请求读取超时 |
| `STELLARMESH_LOGGING_WRITE_TIMEOUT` | `10s` | HTTP 响应写入超时 |
| `STELLARMESH_LOGGING_IDLE_TIMEOUT` | `60s` | HTTP 空闲连接超时 |
| `STELLARMESH_LOGGING_SHUTDOWN_TIMEOUT` | `10s` | 服务关闭排空超时 |
| `STELLARMESH_LOGGING_BATCH_FLUSH_INTERVAL` | `500ms` | 接收队列批量刷新间隔 |
| `STELLARMESH_LOGGING_KAFKA_PUBLISH_TIMEOUT` | `5s` | 单次 Kafka 发布及后台可用性检查超时 |
| `STELLARMESH_LOGGING_KAFKA_REPLAY_INTERVAL` | `5s` | spool 重放间隔 |
| `STELLARMESH_LOGGING_QUEUE_CAPACITY_EVENTS` | `4096` | 尚未获得持久确认的事件容量，最大 `1000000` |
| `STELLARMESH_LOGGING_QUEUE_CAPACITY_BYTES` | `16MiB` | 队列中规范化事件 JSON 的总字节容量，最大 `1GiB` |
| `STELLARMESH_LOGGING_MAX_BATCH_SIZE` | `512` | 发布 Kafka 的目标批量大小，最大 `10000` |
| `STELLARMESH_LOGGING_MAX_BATCH_BYTES` | `4MiB` | 目标规范化 JSON 字节数，最大 `64MiB`；单个请求可以独立超过目标 |
| `STELLARMESH_LOGGING_MAX_REQUEST_EVENTS` | `512` | 单次 HTTP 请求最大事件数，最大 `10000` |
| `STELLARMESH_LOGGING_KAFKA_BROKERS` | `kafka:9092` | 逗号分隔的 broker 地址 |
| `STELLARMESH_LOGGING_KAFKA_TOPIC` | `stellarmesh.logging.events.v1` | 已由基础设施创建的 Topic |
| `STELLARMESH_LOGGING_SPOOL_DIR` | `<data-dir>/spool` | Kafka 失败分段缓冲根目录 |
| `STELLARMESH_LOGGING_SPOOL_MAX_BYTES` | `1GiB` | 三类 spool 共用的数据容量上限，最大 `1TiB` |
| `STELLARMESH_LOGGING_SPOOL_SEGMENT_BYTES` | `16MiB` | 目标分段大小，最大 `64MiB`；单条事件另受 `900KiB` 契约限制 |
| `STELLARMESH_LOGGING_SPOOL_REPLAY_BATCH_SIZE` | `128` | 回放每次 Kafka 发布的事件数，最大 `10000` |

认证文件由业务部署以只读 Secret 挂载，不提交到本仓库。格式如下：

```json
{
  "services": {
    "example-api": [
      "current-token-at-least-32-characters",
      "next-token-at-least-32-characters"
    ]
  }
}
```

所有显式设置的整数、布尔、duration、byte size 和 CSV 都使用严格解析；格式错误、溢出或超过上述边界会直接导致服务启动失败，不会静默退回默认值。所有时间参数最大允许 `24h`。

同一个 service 可以同时配置两个 token，用于滚动轮换；同一 token 不得绑定到不同 service。请求中的每个事件都必须使用与 token 绑定一致的 `service`，否则返回 `403`。轮换顺序是先同时配置新旧 token 并滚动重启 ingester，再切换客户端，最后移除旧 token 并再次滚动重启。

容器必须能写入 `STELLARMESH_LOGGING_DATA_DIR`，且该目录应使用业务项目管理的持久卷。spool 在权限为 `0700` 的 `.staging/` 中准备完整批次，再原子提交到 `batches/`；`ERROR` 和 `AUDIT` 的分段优先回放，但 priority 临时失败不会阻止本轮继续尝试 regular。升级后的服务仍会回放旧版本 `regular/` 与 `priority/` 中的 `.ready.jsonl`，但不会识别业务项目自行实现的其他 JSONL spool。损坏、不兼容或被 publisher 判定为永久失败的 segment 会原子移动到 `quarantine/`，同时写入来源和错误元数据；隔离数据计入容量但不会自动删除，运维确认后才能清理。分段只有全部发布成功才删除，失败重试可能重复发送已经发布的段内事件；ClickHouse 表的事件标识用于降低重复的最终影响，但消费侧仍应按 at-least-once 设计。

服务启动时仍会检查 Topic，但 Kafka 不可用、Topic 暂时不存在或 ACL 检查失败时，只要本地 spool 可以初始化且尚有容量，ingester 会以降级模式启动并通过 spool 持久接收；它不会自行创建 Kafka 资源。后台检查成功后自动恢复 Kafka 发布和重放。单次重放发布与可用性检查都受 `STELLARMESH_LOGGING_KAFKA_PUBLISH_TIMEOUT` 限制。关闭超过 `STELLARMESH_LOGGING_SHUTDOWN_TIMEOUT` 时进程停止等待后台 I/O 并返回失败，由编排器完成进程级回收；此路径不会并发关闭仍被 worker 使用的 publisher。

存活检查使用 `GET /health/live`，就绪检查使用 `GET /health/ready`，原有 `GET /health` 仍作为存活检查兼容入口。就绪状态在 Kafka 发布失败且 spool 写入失败或达到容量上限时变为 `503`，后台 Kafka 检查与回放可以在没有新请求时恢复就绪状态。Prometheus 抓取地址为 `GET /metrics`。SDK 写入使用 `POST /v1/log-events/batch` 和 `X-Logging-Service-Token`；请求体上限为 `1MiB`，其中每条规范化事件不得超过 `900KiB`。请求会等待批次获得 Kafka 全同步副本确认或 spool 原子提交，正式成功状态才是 `202`，两条持久路径均失败时返回 `503`。客户端请求提前取消后，服务仍会处理已经入队的事件，因此重试必须按 at-least-once 接受重复；迁移期客户端也接受状态与 envelope 同为 `200` 的旧服务响应。

## 部署 ClickHouse sink

业务项目从固定 tag 或 digest 引用 `stellarmesh-logging-clickhouse-sink` 镜像。运行时只注入 Kafka 消费权限和 ClickHouse DML 权限，不注入迁移身份。

| 配置键 | 默认值 | 说明 |
| --- | --- | --- |
| `STELLARMESH_LOGGING_KAFKA_BROKERS` | `kafka:9092` | 逗号分隔的 broker 地址 |
| `STELLARMESH_LOGGING_KAFKA_TOPIC` | `stellarmesh.logging.events.v1` | 与接收服务一致的 Topic |
| `STELLARMESH_LOGGING_KAFKA_DLQ_TOPIC` | `stellarmesh.logging.events.v1.dlq` | 必须预先创建且不能与源 Topic 相同 |
| `STELLARMESH_LOGGING_WRITER_GROUP_ID` | `stellarmesh-logging-clickhouse` | 项目独立的 consumer group |
| `STELLARMESH_LOGGING_CLICKHOUSE_HTTP_URL` | `http://clickhouse:8123` | ClickHouse HTTP 地址 |
| `STELLARMESH_LOGGING_CLICKHOUSE_DATABASE` | 无 | 必填；项目 database |
| `STELLARMESH_LOGGING_CLICKHOUSE_USER` | 无 | 必填；低权限运行时用户 |
| `STELLARMESH_LOGGING_CLICKHOUSE_PASSWORD` | 空 | 运行时用户密码 |
| `STELLARMESH_LOGGING_WRITER_BATCH_SIZE` | `500` | ClickHouse 写入批量大小，最大 `10000` |
| `STELLARMESH_LOGGING_WRITER_BATCH_MAX_BYTES` | `16MiB` | 单批 Kafka key/value 上限，最大 `64MiB`，且不能小于源消息上限 |
| `STELLARMESH_LOGGING_WRITER_FLUSH_INTERVAL` | `1s` | 不满一批时从首条消息开始计算的最大等待时间 |
| `STELLARMESH_LOGGING_WRITER_RETRY_INTERVAL` | `1s` | 下游或 Kafka 操作失败后的重试间隔 |
| `STELLARMESH_LOGGING_WRITER_SHUTDOWN_TIMEOUT` | `10s` | 关闭时最后一批的独立排空超时 |
| `STELLARMESH_LOGGING_WRITER_HTTP_TIMEOUT` | `5s` | ClickHouse 请求超时 |
| `STELLARMESH_LOGGING_WRITER_MAX_SOURCE_MESSAGE_BYTES` | `1MiB` | Kafka 单条 key/value 硬判定上限，最大允许配置 `1MiB` |
| `STELLARMESH_LOGGING_WRITER_OBSERVABILITY_ADDR` | `:8092` | sink 健康检查与 Prometheus 监听地址 |

ingester、ClickHouse sink 和后续 DLQ producer 共用以下 Kafka 安全配置：

| 配置键 | 默认值 | 说明 |
| --- | --- | --- |
| `STELLARMESH_LOGGING_KAFKA_SECURITY_PROTOCOL` | `PLAINTEXT` | `PLAINTEXT`、`TLS`、`SASL_PLAINTEXT` 或 `SASL_TLS` |
| `STELLARMESH_LOGGING_KAFKA_SASL_MECHANISM` | 空 | `PLAIN`、`SCRAM-SHA-256` 或 `SCRAM-SHA-512` |
| `STELLARMESH_LOGGING_KAFKA_USERNAME` | 空 | SASL 用户名 |
| `STELLARMESH_LOGGING_KAFKA_PASSWORD` | 空 | SASL 密码，由 Secret 注入 |
| `STELLARMESH_LOGGING_KAFKA_TLS_CA_FILE` | 空 | 自定义 CA 文件路径；为空时使用系统 CA |
| `STELLARMESH_LOGGING_KAFKA_TLS_CERT_FILE` | 空 | mTLS 客户端证书路径 |
| `STELLARMESH_LOGGING_KAFKA_TLS_KEY_FILE` | 空 | mTLS 客户端私钥路径，必须与证书同时配置 |
| `STELLARMESH_LOGGING_KAFKA_TLS_SERVER_NAME` | 空 | TLS server name 覆盖值 |

TLS 最低版本为 1.2，服务不提供跳过证书校验的配置。使用 `SASL_PLAINTEXT` 时凭据不受 TLS 保护，只应在已有可信网络加密层的环境使用。

运行时用户需要对既有 `log_events` 表执行插入，并能完成 `SELECT 1` 连通性检查，但不应具备创建 database、用户或表的权限。Kafka 身份需要消费源 Topic、使用指定 consumer group，并生产 DLQ Topic；不需要创建或修改 Topic 的管理权限。

sink 会严格解析每条源消息。有效事件写入 ClickHouse；普通无效事件按 `contracts/logging/v1/dead-letter.schema.json` 写入 DLQ，原始 key 和 payload 使用 Base64 保存；超过源消息上限的消息按 `contracts/logging/v1/dead-letter-v2.schema.json` 写入同一 DLQ Topic，只保存源坐标、字节数和 SHA-256，不复制原始内容。处理顺序固定为 ClickHouse 插入、DLQ 发布、源 offset 提交，三步全部成功才清空内存批次。任何一步失败都会重试整批，因此 ClickHouse 与 DLQ 都可能出现重复，不能依赖“恰好一次”语义。

源 Topic 的 broker `max.message.bytes` 必须与 `1MiB` 契约保持一致，不能把 Kafka reader 的 `MaxBytes` 当作拒绝超大消息的安全边界：Kafka 为保证消费进度仍可能返回第一个更大的 record batch。sink 将 reader 预取容量固定为一条，fetch 后再按 key/value 总字节执行硬判定；超限消息只生成 DLQ v2 摘要。消费批次的消息数和 payload 字节预算限制应用持有的载荷，不是进程 RSS 硬上限，JSON 解析、Base64、Kafka 协议缓冲和 ClickHouse 编码仍会产生有界临时分配。

普通无效消息使用 DLQ v1，Base64 会扩大记录，因此 DLQ Topic 的 `max.message.bytes` 应至少覆盖“源消息上限的 `4/3` 加 `16KiB` 协议余量”，例如源消息上限为 `1MiB` 时应允许至少约 `1.35MiB`。DLQ 可能保存原始敏感载荷，必须使用受限 ACL、加密传输、明确保留期和独立告警，不得向普通业务消费者开放。

容器观测端口默认为 `8092`。存活检查使用 `GET /health/live`，就绪检查使用 `GET /health/ready`，Prometheus 使用 `GET /metrics`。启动期间、Kafka 拉取失败、ClickHouse 插入失败、DLQ 发布失败或 offset 提交失败时，就绪检查返回 `503`；恢复并成功处理后返回 `200`。

## 执行迁移制品

迁移不是业务 Compose 中的常驻服务，也不是日志接收服务或 sink 的启动命令。`server-infrastructure` 应在资源准备、备份和 preflight 完成后，以单实例一次性任务运行固定 digest 的 `stellarmesh-logging-clickhouse-migrate` 镜像。

开发环境可以由业务项目用自己的连接信息执行同一制品，例如：

```sh
docker run --rm stellarmesh-logging-clickhouse-migrate:0.1.0 \
  -database 'clickhouse://clickhouse:9000?username=migrator&password=example&database=logging&x-multi-statement=true' \
  up
```

示例中的值只用于说明参数形式。生产环境不得把真实密码写入仓库或可公开读取的命令记录，应由编排器从受保护密钥注入迁移任务。迁移身份只在任务期间可用，常驻服务不能复用它。

生产执行至少需要验证：

1. database、Kafka Topic 和三类身份已经由资源编排创建；
2. 迁移镜像与服务镜像来自同一 Git 版本且固定 digest；
3. 当前 revision、目标 revision、备份和回滚策略已确认；
4. 迁移任务只有一个实例，失败会返回非零状态并阻止发布；
5. 迁移后 `log_events` 为预期 revision，运行时用户可以插入但不能执行 DDL；
6. 再部署接收服务和 sink，并执行健康检查与端到端最小写入测试。

## 监控与故障处理

业务项目至少应监控：

- SDK `drop_handler` 或 `OnDrop` 计数；
- logging-service 的 `400`、`401`、`413`、`503`、readiness 和队列排空失败；
- `stellarmesh_logging_ingester_queue_events`、`stellarmesh_logging_ingester_queue_bytes`、Kafka 发布失败计数、regular/priority/quarantine spool 字节数与重放结果计数；
- Kafka consumer lag；
- `stellarmesh_logging_clickhouse_sink_pending_messages`、`stellarmesh_logging_clickhouse_sink_pending_bytes`、各阶段失败计数和 sink readiness；
- ClickHouse 批量插入错误、DLQ 产生速率、DLQ lag、DLQ 保留容量和重复记录；
- 应用关闭时 SDK drain 是否超时。

收到 `202` 表示事件已由 Kafka 全同步副本确认，或已经原子提交到 logging-service 的持久 spool；它不表示 ClickHouse 已经写入。后续 Kafka 消费、ClickHouse 写入和 offset 提交仍按 at-least-once 重试，所以不能只用 HTTP 成功率判断最终查询链路是否完整。审计类业务如果要求业务事务与审计记录原子提交，仍应设计事务性审计存储，不能把独立日志链路当作业务事务的一部分。
