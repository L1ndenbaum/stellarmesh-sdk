# 接入 SDK

本文件说明业务项目接入 SDK、日志接收服务、对象存储服务、ClickHouse sink 和迁移制品的完整流程。只接入日志客户端时，可直接阅读 [Go Logging SDK](sdk/go/logging.md) 或 [Python SDK 接入教程](sdk/python/README.md)；父 Go SDK见[Go 父 SDK 接入教程](sdk/go/README.md)，Kafka 基础能力见 [Go Kafka SDK](sdk/go/kafka.md)，网关能力见 [Go 网关 SDK](sdk/go/gateway.md)，对象存储接入分别阅读 [Go 对象存储 SDK](sdk/go/object-storage.md)、[Python 对象存储 SDK](sdk/python/storage.md) 和 [storage-service 部署文档](storage-service.md)。

## 接入前准备

业务项目需要自行管理开发环境和项目级部署配置。本仓库不提供 Compose 或 `.env` 模板。接入前应明确以下值：

- 当前项目的日志接收服务地址；
- 项目独立的服务令牌；
- 稳定且可区分的业务 `service` 名称；
- 项目独立的 Kafka Topic、consumer group 和 ACL；
- 项目独立的 Kafka DLQ Topic、保留策略和 sink 生产权限；
- 项目独立的 ClickHouse database、迁移身份与运行时身份；
- 日志接收服务本地 spool 的持久化目录。
- 项目对象存储的逻辑 namespace、Bucket、Prefix 和最小权限 IAM 或 MinIO Policy；
- 项目独立的 `storage-service` 访问文件、轮换 token 和可选的 S3-compatible Endpoint；

开发环境可由业务项目自行用 Compose、测试容器或共享开发基础设施提供。生产资源必须通过 `server-infrastructure` 声明和编排。

## Go 项目

安装固定版本：

```sh
go get github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging@v0.2.0
```

在应用启动时构造一个进程级客户端，并优先接入标准库 `log/slog`：

```go
package main

import (
    "context"
    "log"
    "log/slog"
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
    handler, err := logging.NewSlogHandler(client, logging.SlogHandlerConfig{
        Service: "example-api",
        MinimumLevel: logging.LevelInfo,
        TraceIDProvider: func(ctx context.Context) string {
            return traceIDFromProjectContext(ctx)
        },
    })
    if err != nil {
        log.Fatal(err)
    }

    logger := slog.New(handler)
    ctx := context.Background()
    logger.InfoContext(ctx, "order created", slog.String("order_id", "123"))

    shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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

- `slog.Handler` 在调用栈内只完成级别过滤、事件转换和非阻塞入队，不执行 HTTP；
- Go 构造函数会立即拒绝非法 URL、空 token、空 service 和负数限制，不把配置错误延迟到后台 worker；
- 根级 `trace_id` attribute 优先于 `TraceIDProvider`，嵌套组中的同名字段只是 metadata；
- `OnDrop` 只应执行轻量、不会递归调用同一 logger 的降级动作；
- HTTP handler、worker 和定时任务共享同一个客户端即可，不要为每次请求创建后台 worker。

结构化 `Logger` 的普通方法可通过 `LoggerConfig.MinimumLevel` 设置远程最低级别；零值为 `DEBUG`。这些方法和 `slog.Handler` 始终生成 `kind=LOG`。审计事件应显式调用 `Logger.Audit(ctx, level, message, traceID, metadata)`；客户端整体启用时，它生成的 `kind=AUDIT` 绕过普通日志最低级别过滤。需要直接提交已构造事件时使用 `Client.Enqueue`。`Emitter.Emit` 的 context 只用于入队前的 Handler、Logger 和 trace 提取，事件入队后由客户端生命周期管理，不继承业务请求取消信号。

### Go 网关项目

项目不再复制 gateway 内部目录，而是在自己的 `main.go` 中通过 `gateway.New` 组装路由、upstream、JWT、Redis 限流、CORS、健康检查和访问日志。业务仓库继续拥有配置读取、路由表、监听端口、优雅关闭、Compose 和环境变量；本仓库只提供普通 `http.Handler` 和可注入组件。

```sh
go get github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/gateway@v0.3.0
```

生产配置应明确可信代理 CIDR。没有可信代理配置时，SDK 只使用 `RemoteAddr`，不会读取 `X-Forwarded-For`。所有静态受保护路由必须配置 Authenticator；Redis 限流器或其他安全决策依赖发生错误时返回 `503`，不会退化为放行。

Gateway `v0.3.0` 默认通过标准库 `slog.Default()` 输出访问日志，项目可以用 `slog.SetDefault` 统一格式、等级和 CLI 输出，也可以通过 `WithoutAccessLog` 关闭。Gateway Core 不知道远程日志、Sink 或持久化语义；需要 Stellarmesh Logging 时，安装独立 `loggingadapter@v0.2.0`，把访问记录转换为 Logging v2 的 `kind=LOG` 并交给项目管理的 `logging.Emitter`。Adapter 不推断审计语义，其失败仍属于旁路失败，不改变网关响应。

完整构造示例、Option 列表、错误状态和验证要求见[Go 网关 SDK 接入教程](sdk/go/gateway.md)。

## Python 项目

本地开发可从仓库子目录安装：

```sh
pip install ./sdk/python/logging
```

发布到 PyPI 后，应固定包版本：

```sh
pip install stellarmesh-logging==0.2.0
```

在应用生命周期入口显式配置客户端：

```python
import logging

from stellarmesh_logging import (
    Client,
    ClientConfig,
    StellarmeshHandler,
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
root_logger = logging.getLogger()
root_logger.setLevel(logging.INFO)
root_logger.addHandler(StellarmeshHandler(client))
logger = logging.getLogger(__name__)

logger.info("job %s started", "job-123", extra={"component": "scheduler"})


async def application_shutdown() -> None:
    await shutdown_logging(timeout=10.0)
```

业务项目仍需按照自身运行环境设置 logger 的有效级别；Handler 不创建控制台输出，始终生成 `kind=LOG`，`ClientConfig.minimum_level` 只过滤真正进入远程队列的普通日志。审计事件必须通过 `get_logger().audit()` 显式生成，并继续选择 `INFO`、`WARNING` 或 `ERROR` 等严重程度。同步入口使用 `shutdown_logging_sync(timeout=10.0)`。存在 asyncio event loop 时必须 `await shutdown_logging()`，不能调用同步包装器。标准 logging 方法返回 `None`；需要获取是否入队的布尔值或使用 `audit`、`bind` 时，可以继续使用 SDK 的 `get_logger()` 日志门面。

`drop_handler` 同样不能再调用当前远端 logger，避免失败时递归。若业务已有 trace 上下文，应在 provider 中适配；SDK 不直接依赖 FastAPI、Django、Flask、OpenTelemetry 或业务自定义 context。

## 接入对象存储

对象存储有两种接入路径，二者共享相容的 key、Metadata、Checksum 和稳定错误语义，但只有控制面路径使用 Storage v1 HTTP 契约：

- Go 服务需要在进程内读取或写入对象时，直接使用 `objectstorage/s3store`，由应用启动配置把一个客户端绑定到一个 Bucket 与 Prefix；
- Python 或其他不应持有对象存储凭据的客户端，通过项目自己的 `storage-service` 获取预签名请求，再直接与 S3 或 MinIO 传输对象字节。

Go 进程内路径安装独立 Module：

```sh
go get github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/objectstorage@v0.1.0
```

Go 进程内客户端使用标准 AWS 凭据链，不应把 Bucket 暴露为每次业务调用的参数。AWS 模式不设置自定义 Endpoint；MinIO 模式显式设置内部 Endpoint、客户端可访问的 `PresignEndpoint` 和 `UsePathStyle=true`。`Check` 只执行只读可访问性检查，不创建 Bucket。构造、Range、Checksum、错误映射和显式 Multipart 示例见 [Go 对象存储 SDK](sdk/go/object-storage.md)。

`contracts/storage/v1` 是跨语言控制面协议的唯一来源。`storage-service` 内部的 Go DTO 与访问策略不属于公共 Go SDK；当前没有独立发布的 Go Storage v1 HTTP Client。Go 项目需要直接访问对象存储时使用 `objectstorage`，需要通过控制面访问时应按 OpenAPI 生成或实现项目客户端，不能导入 `services/storage/internal/storagev1`。

Python 包独立安装，不能用日志包替代：

```sh
pip install stellarmesh-storage==0.1.1
```

```python
from stellarmesh_storage import Client, ClientConfig

config = ClientConfig(
    base_url="http://storage-service:8090",
    token="storage-project-service-token-00000001",
    timeout_seconds=5.0,
    max_attempts=3,
)

with Client(config) as client:
    client.upload_file(
        "documents",
        "reports/2026.pdf",
        "/work/report.pdf",
        content_type="application/pdf",
    )
    client.download_file(
        "documents",
        "reports/2026.pdf",
        "/work/result.pdf",
    )
```

`storage-service` 必须一项目一实例，使用该项目自己的 IAM Role、Web Identity 或 MinIO 项目凭据。只读访问文件声明 namespace 到 Bucket/Prefix 的映射，以及 principal token 对 `read`、`write`、`delete` 的授权。服务只提供 Stat、Delete、Presign GET/PUT 和显式 Multipart 控制面，不提供对象字节代理路由。

部署时至少配置 `STELLARMESH_STORAGE_ACCESS_FILE` 和 AWS Region；MinIO 还需要 `STELLARMESH_STORAGE_ENDPOINT`、通常需要 `STELLARMESH_STORAGE_USE_PATH_STYLE=true`，若客户端不能访问内部地址则配置 `STELLARMESH_STORAGE_PRESIGN_ENDPOINT`。存活检查使用 `/health/live`，就绪检查使用 `/health/ready`，指标使用 `/metrics`。readiness 只有在全部 namespace 可访问时才成功；not-ready 时控制面 fail-close 返回 `503`。

Bucket、Policy、CORS、Lifecycle、Versioning、ACL 和 Secret 注入属于业务部署或 `server-infrastructure`。SDK 与服务不管理这些资源，也不提供跨项目中央凭据池。每个业务项目应先在自己的分支完成接入和回归；本仓库发布制品不会自动修改或迁移业务仓库。

## 部署日志接收服务

业务项目从固定 tag 或 digest 引用公开的 `ghcr.io/l1ndenbaum/stellarmesh-sdk/logging-service` 镜像，并自行管理网络、端口、持久卷和配置注入。公开镜像支持匿名拉取，生产仍应固定已验证 digest。服务配置如下：

| 配置键 | 默认值 | 说明 |
| --- | --- | --- |
| `STELLARMESH_LOGGING_ADDR` | `:8091` | HTTP 监听地址 |
| `STELLARMESH_LOGGING_AUTH_FILE` | 无 | 必填；挂载的 service-token 绑定配置 |
| `STELLARMESH_LOGGING_DATA_DIR` | `/var/lib/stellarmesh-logging` | 本地持久化根目录 |
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
| `STELLARMESH_LOGGING_KAFKA_TOPIC` | `stellarmesh.logging.events.v2` | 已由基础设施创建的 v2 Topic |
| `STELLARMESH_LOGGING_SPOOL_DIR` | `<data-dir>/spool` | Kafka 失败分段缓冲根目录 |
| `STELLARMESH_LOGGING_SPOOL_MAX_BYTES` | `1GiB` | regular、priority、quarantine 实际文件与 live segment 隔离替换预留的共同硬上限；最小 `1908738` 字节，最大 `1TiB` |
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

容器必须能写入 `STELLARMESH_LOGGING_DATA_DIR`，且该目录应使用业务项目管理的持久卷。spool 在权限为 `0700` 的 `.staging/` 中准备完整批次，再原子提交到 `batches/`；`kind=AUDIT` 或 `level=ERROR` 的分段优先回放，但 priority 临时失败不会阻止本轮继续尝试 regular。本版本没有为 priority 预留独立容量，普通日志占满共同 spool 后，高优先级事件仍可能无法写入，因此应同时配置生产最低日志级别、容量告警和隔离数据处置流程。

v2 spool 根目录必须含有内容为 `stellarmesh-logging-spool-v2` 的 `FORMAT` 标记。全新空目录会原子创建标记；已有正确标记正常恢复；错误标记，或没有标记但存在 live segment 的目录会阻止启动。升级前必须排空 v1 spool，或由运维整体移出旧数据目录，不能依赖 v2 服务解析或 quarantine 合法的 v1 segment。v2 中真正损坏或永久不可发布的 segment 继续进入 `quarantine/`；回放会递归缩小失败批次，只隔离具体失败记录，同段正常记录继续发布。每个 live segment 在容量账本中额外预留等同自身大小的替换空间和 `64KiB` 元数据空间，隔离临界路径不会先突破 `STELLARMESH_LOGGING_SPOOL_MAX_BYTES` 再删除源文件；因此该配置是物理数据与安全预留的共同预算，不等于可接收事件的净容量。隔离数据计入容量但不会自动删除，运维确认后才能清理。暂时失败时保留原分段，重试可能重复发送已经发布的段内事件；ClickHouse 表的事件标识用于降低重复的最终影响，但消费侧仍应按 at-least-once 设计。

服务启动时仍会检查 Topic，但 Kafka 不可用、Topic 暂时不存在或 ACL 检查失败时，只要本地 spool 可以初始化且尚有容量，ingester 会以降级模式启动并通过 spool 持久接收；它不会自行创建 Kafka 资源。Topic 检查并行探测全部去重后的非空 broker，任一 broker 能访问目标 Topic 且返回 partition 即成功；全部失败时返回聚合错误。后台检查成功后自动恢复 Kafka 发布和重放，即使 spool 为空也能恢复 readiness。单次重放发布与可用性检查都受 `STELLARMESH_LOGGING_KAFKA_PUBLISH_TIMEOUT` 限制。

Kafka 或 spool 持久确认成功后，服务才把事件副本交给异步控制台 sink。控制台每个事件输出一行紧凑 JSON；消息中的换行、ANSI 和控制字符由 JSON 编码转义。控制台队列同时限制事件数和字节数，队列满、编码失败或 stdout 写入失败只丢弃控制台副本，不改变 HTTP、Kafka 或 spool 结果。关闭时先停止 HTTP 并排空持久化队列，再在共同 deadline 内尝试排空控制台；阻塞 stdout 不会阻止进程退出。已有部署继续携带 `STELLARMESH_LOGGING_CONSOLE_COLOR` 不会导致启动失败，但该变量已被忽略并应从部署配置删除。

默认客户端 HTTP 超时为 `7s`，用于覆盖默认 `500ms` 聚合等待与 `5s` Kafka 发布超时。如果提高这两个服务端参数，调用方必须同步提高 Go `Timeout` 或 Python `timeout_seconds`。客户端支持整数秒与 HTTP-date 形式的 `Retry-After`，默认最多等待 `30s`；关闭信号可以中断在途请求和重试等待。关闭超过 `STELLARMESH_LOGGING_SHUTDOWN_TIMEOUT` 时服务停止等待后台 I/O 并返回失败，由编排器完成进程级回收；此路径不会并发关闭仍被 worker 使用的 publisher。

存活检查使用 `GET /health/live`，就绪检查使用 `GET /health/ready`，`GET /health` 仍作为存活检查入口。就绪状态在 Kafka 发布失败且 spool 写入失败或达到容量上限时变为 `503`，后台 Kafka 检查与回放可以在没有新请求时恢复就绪状态。Prometheus 抓取地址为 `GET /metrics`。SDK 写入使用 `POST /v2/log-events/batch` 和 `X-Logging-Service-Token`；v1 路由不存在。请求体上限为 `1MiB`，其中每条规范化事件不得超过 `900KiB`。Kafka key/value 另受 `960KiB` 预算约束，分区键使用 `trace_id` 的 SHA-256 摘要或 `event_id`，并为 Kafka 协议开销保留余量。请求会等待批次获得 Kafka 全同步副本确认或 spool 原子提交，成功状态为 `202`，两条持久路径均失败时返回 `503`。客户端请求提前取消后，服务仍会处理已经入队的事件，因此重试必须按 at-least-once 接受重复。

## 部署 ClickHouse sink

业务项目从固定 tag 或 digest 引用 `ghcr.io/l1ndenbaum/stellarmesh-sdk/logging-clickhouse-sink` 镜像。运行时只注入 Kafka 消费权限和 ClickHouse DML 权限，不注入迁移身份。

| 配置键 | 默认值 | 说明 |
| --- | --- | --- |
| `STELLARMESH_LOGGING_KAFKA_BROKERS` | `kafka:9092` | 逗号分隔的 broker 地址 |
| `STELLARMESH_LOGGING_KAFKA_TOPIC` | `stellarmesh.logging.events.v2` | 与接收服务一致的 v2 Topic |
| `STELLARMESH_LOGGING_KAFKA_DLQ_TOPIC` | `stellarmesh.logging.events.v2.dlq` | 必须预先创建且不能与源 Topic 相同 |
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

sink 只严格解析 Logging v2，不解析 v1 Kafka 消息，也不从 `level=AUDIT` 推断事件种类。有效事件把 `kind` 和标准 `level` 一起写入 ClickHouse；普通无效事件按 `contracts/logging/v2/dead-letter.schema.json` 写入 DLQ，原始 key 和 payload 使用 Base64 保存；超过源消息上限的消息按 `contracts/logging/v2/dead-letter-v2.schema.json` 写入同一 DLQ Topic，只保存源坐标、字节数和 SHA-256，不复制原始内容。处理顺序固定为 ClickHouse 插入、DLQ 发布、源 offset 提交，三步全部成功才清空内存批次。任何一步失败都会重试整批，因此 ClickHouse 与 DLQ 都可能出现重复，不能依赖“恰好一次”语义。v1 Topic 必须在切换前排空，v2 sink 不接管 v1 backlog。

源 Topic 的 broker `max.message.bytes` 必须与 `1MiB` 契约保持一致，不能把 Kafka reader 的 `MaxBytes` 当作拒绝超大消息的安全边界：Kafka 为保证消费进度仍可能返回第一个更大的 record batch。sink 将 reader 预取容量固定为一条，fetch 后再按 key/value 总字节执行硬判定；超限消息只生成 DLQ v2 摘要。消费批次的消息数和 payload 字节预算限制应用持有的载荷，不是进程 RSS 硬上限，JSON 解析、Base64、Kafka 协议缓冲和 ClickHouse 编码仍会产生有界临时分配。

普通无效消息使用完整载荷 DLQ 格式，其 `schema_version=1` 只表示 DLQ 载荷形式，不代表 LogEvent v1。Base64 会扩大记录，因此 DLQ Topic 的 `max.message.bytes` 应至少覆盖“源消息上限的 `4/3` 加 `16KiB` 协议余量”，例如源消息上限为 `1MiB` 时应允许至少约 `1.35MiB`。DLQ 可能保存原始敏感载荷，必须使用受限 ACL、加密传输、明确保留期和独立告警，不得向普通业务消费者开放。

容器观测端口默认为 `8092`。存活检查使用 `GET /health/live`，就绪检查使用 `GET /health/ready`，Prometheus 使用 `GET /metrics`。启动期间、Kafka 拉取失败、ClickHouse 插入失败、DLQ 发布失败或 offset 提交失败时，就绪检查返回 `503`；恢复并成功处理后返回 `200`。

## 执行迁移制品

迁移不是业务 Compose 中的常驻服务，也不是日志接收服务或 sink 的启动命令。`server-infrastructure` 应在资源准备、备份和 preflight 完成后，以单实例一次性任务运行固定 digest 的 `ghcr.io/l1ndenbaum/stellarmesh-sdk/logging-clickhouse-migrate` 镜像。

开发环境可以由业务项目用自己的连接信息执行同一制品，例如：

```sh
docker run --rm ghcr.io/l1ndenbaum/stellarmesh-sdk/logging-clickhouse-migrate:0.2.0 \
  -database 'clickhouse://clickhouse:9000?username=migrator&password=example&database=logging&x-multi-statement=true' \
  up
```

示例中的值只用于说明参数形式。生产环境不得把真实密码写入仓库或可公开读取的命令记录，应由编排器从受保护密钥注入迁移任务。迁移身份只在任务期间可用，常驻服务不能复用它。

Logging v2 的 revision `000002` 会同步回填历史 `kind`，并把旧 `level=AUDIT` 改为 `level=INFO`。降级会把 `kind=AUDIT` 恢复为旧 AUDIT level 后删除 `kind`。up/down 都可能扫描和改写历史分区，执行前必须备份，并检查表大小、分区、当前 revision 和 `distinct level`；未知历史 level 或 kind 会让迁移 fail-close。

生产执行至少需要验证：

1. database、Kafka Topic 和三类身份已经由资源编排创建；
2. 迁移镜像与服务镜像来自同一 Git 版本且固定 digest；
3. 当前 revision、目标 revision、备份和回滚策略已确认；
4. 迁移任务只有一个实例，失败会返回非零状态并阻止发布；
5. 迁移后 `log_events` 为预期 revision，运行时用户可以插入但不能执行 DDL；
6. 再部署接收服务和 sink，并执行健康检查与端到端最小写入测试。

### v1 到 v2 的维护窗口顺序

v1 与 v2 不提供双读或双写兼容，不能滚动混用。固定切换顺序为：

1. 停止 v1 producers，并排空客户端、logging-service 接收队列、v1 Kafka lag 和 v1 spool；
2. 备份 ClickHouse，确认历史 `level` 只有 `DEBUG`、`INFO`、`WARNING`、`ERROR`、`AUDIT`；
3. 创建 v2 Topic、v2 DLQ Topic 和对应 ACL；
4. 运行同一发布版本的 migrate 镜像升级到 revision 2，失败时阻止继续发布；
5. 依次启动 v2 sink、v2 logging-service，等待 readiness，再启动 v2 producers；
6. 验证 `LOG+INFO`、`LOG+ERROR`、`AUDIT+INFO`、DLQ、spool 和 ClickHouse 的 `kind/level`；
7. 确认稳定后再按运维策略处置已经排空并移出的 v1 spool 与 Topic。

回滚时必须先停止 v2 producers，排空 v2 客户端、服务队列、Kafka lag 和 spool，再执行 down migration，并整体恢复 v1 sink、service、Topic 与 producers。只回滚数据库、单个服务或某个 SDK 都会造成契约不一致。

## 监控与故障处理

业务项目至少应监控：

- SDK `drop_handler` 或 `OnDrop` 计数；
- logging-service 的 `400`、`401`、`413`、`503`、readiness 和队列排空失败；
- `stellarmesh_logging_ingester_queue_events`、`stellarmesh_logging_ingester_queue_bytes`、Kafka 发布失败计数、regular/priority/quarantine spool 字节数与重放结果计数；
- `stellarmesh_logging_ingester_console_events_total{result}`，其中 `result` 只允许 `emitted`、`dropped`、`failed`；
- Kafka consumer lag；
- `stellarmesh_logging_clickhouse_sink_pending_messages`、`stellarmesh_logging_clickhouse_sink_pending_bytes`、各阶段失败计数和 sink readiness；
- ClickHouse 批量插入错误、DLQ 产生速率、DLQ lag、DLQ 保留容量和重复记录；
- 应用关闭时 SDK drain 是否超时。
- `storage-service` 的 `401`、`403`、`413`、`503`、readiness 切换和优雅关闭；
- 对象存储操作按有限 operation/result 标签统计的失败率与延迟，以及 S3 或 MinIO 自身的容量、配额和可用性；

收到 `202` 表示事件已由 Kafka 全同步副本确认，或已经原子提交到 logging-service 的持久 spool；它不表示 ClickHouse 已经写入。后续 Kafka 消费、ClickHouse 写入和 offset 提交仍按 at-least-once 重试，所以不能只用 HTTP 成功率判断最终查询链路是否完整。审计类业务如果要求业务事务与审计记录原子提交，仍应设计事务性审计存储，不能把独立日志链路当作业务事务的一部分。
