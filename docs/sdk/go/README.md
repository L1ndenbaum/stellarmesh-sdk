# Go SDK 接入教程

本教程对应 Go module `github.com/L1ndenbaum/stellarmesh-sdk/sdk/go`，适用于 Go 1.24 及以上版本。业务日志接入使用 `logging` package；需要复用路由、鉴权、限流和反向代理时，阅读[Go 网关 SDK 接入教程](gateway.md)；需要进程内访问 AWS S3 或 MinIO 时，阅读[Go 对象存储 SDK 接入教程](object-storage.md)。

## 1. 安装固定版本

正式接入应使用 `sdk/go/vX.Y.Z` 子模块 tag：

```sh
go get github.com/L1ndenbaum/stellarmesh-sdk/sdk/go@v0.1.1
go mod tidy
```

如果 SDK 尚未发布，只在本地联调时可以临时指向相邻源码目录：

```sh
go mod edit -replace github.com/L1ndenbaum/stellarmesh-sdk/sdk/go=../stellarmesh-sdk/sdk/go
go mod tidy
```

本地 `replace` 不应作为生产依赖提交。推送业务仓库前应恢复为已发布版本，并检查 `go.mod` 与 `go.sum`。

## 2. 准备业务配置

SDK 不直接读取环境变量或业务配置文件。业务项目应在自己的配置层解析以下值，再传给 SDK：

```go
type LoggingConfig struct {
    BaseURL string
    Token   string
    Service string
}
```

- `BaseURL` 是 `logging-service` 的 HTTP 根地址，不包含 `/v1/log-events/batch`；
- `Token` 必须来自 Secret，不得写入源码或日志；
- `Service` 是当前进程发送日志时使用的稳定身份，必须与 token 的授权绑定一致、非空且没有首尾空白。

## 3. 创建进程级客户端和 `slog.Logger`

一个业务进程通常只创建一个 `Client`。HTTP handler、后台 worker 和定时任务可以共享它，不要为每个请求创建新的后台发送线程。新项目推荐通过标准库 `log/slog` 接入；已有项目可以继续使用 SDK 原有的 `Logger`，公开 API 保持兼容。

```go
package applogging

import (
    "context"
    "fmt"
    "io"
    "log"
    "log/slog"
    "time"

    logging "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging"
)

type Config struct {
    BaseURL string
    Token   string
    Service string
}

type Runtime struct {
    Client *logging.Client
    Logger *slog.Logger
}

func New(config Config, fallback io.Writer) (*Runtime, error) {
    client, err := logging.NewClient(logging.ClientConfig{
        BaseURL: config.BaseURL,
        Token:   config.Token,
        OnDrop: func(event logging.Event, dropErr error) {
            log.Printf(
                "remote log dropped: event_id=%s service=%s err=%v",
                event.EventID,
                event.Service,
                dropErr,
            )
        },
        FallbackWriter: fallback,
    })
    if err != nil {
        return nil, fmt.Errorf("create logging client: %w", err)
    }

    handler, err := logging.NewSlogHandler(client, logging.SlogHandlerConfig{
        Service: config.Service,
        MinimumLevel: logging.LevelInfo,
        TraceIDProvider: func(ctx context.Context) string {
            return TraceIDFromContext(ctx)
        },
    })
    if err != nil {
        closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
        defer cancel()
        _ = client.Close(closeCtx)
        return nil, fmt.Errorf("create logging slog handler: %w", err)
    }
    return &Runtime{Client: client, Logger: slog.New(handler)}, nil
}

func TraceIDFromContext(context.Context) string {
    return ""
}
```

示例中的 `TraceIDFromContext` 是业务适配边界。SDK 不依赖业务请求上下文或 OpenTelemetry；如果项目已有 trace，应在这里从调用者传入的 `context.Context` 中提取。

`OnDrop` 必须轻量、线程安全，并且不能再次调用同一个远端 logger，否则网络失败时可能递归。没有配置 `OnDrop` 时，SDK 会通过 `FallbackWriter` 限频输出降级警告；如果两者都未配置，则使用标准错误输出。

## 4. 写入结构化日志

标准 `slog.Logger` 支持 `With`、`WithGroup`、`LogValuer` 和调用方 `context.Context`：

```go
func HandleOrder(ctx context.Context, logger *slog.Logger, orderID string) {
    logger.InfoContext(
        ctx,
        "order created",
        slog.String("order_id", orderID),
        slog.String("component", "order-handler"),
    )
    logger.Log(ctx, logging.SlogLevelAudit, "order approved", slog.String("order_id", orderID))
}
```

`SlogHandlerConfig.MinimumLevel` 的零值默认是 `INFO`。标准级别依次映射为 `DEBUG`、`INFO`、`WARNING`、`ERROR`，精确的 `logging.SlogLevelAudit` 映射为 `AUDIT`。`Enabled` 会在构造事件和清洗 metadata 以前过滤低级别记录。根级 `trace_id` attribute 优先作为事件 trace，不存在时才调用 `TraceIDProvider(ctx)`；嵌套组中的同名字段只是普通 metadata。`service` 只能来自 Handler 配置，attribute 不能覆盖服务身份。开启 `AddSource` 后，源文件和行号写入 metadata。

Handler 只完成转换和非阻塞入队，不在日志调用栈内执行 HTTP。metadata 会经过深度、序列长度、字符串长度和敏感键处理：敏感 key 会先转小写并去除非字母数字字符，所以 `apiKey`、`api_key`、`api-key` 和大小写变体都会命中；`error` 转成受限的 `Error()` 文本；大整数不会经 `float64` 丢失精度；非有限浮点数变为 `[UNSERIALIZABLE]`。业务代码仍不应主动把 Secret 放入日志。标准 `slog` 方法没有入队布尔返回值；队列失败由客户端 `OnDrop` 或限频 fallback warning 观测。

原有 `Logger` 仍提供 `Debug`、`Info`、`Warning`、`Error` 和 `Audit`，这些方法返回是否成功入队。`LoggerConfig.MinimumLevel` 的零值默认是 `DEBUG`，保持 `v0.1.0` 行为。直接构造好 `Event` 的调用方可以使用 `Client.Enqueue(event)`；兼容接口 `Emit(ctx, event)` 会调用同一入口。

旧 `Logger` 与 `Enqueue` 的返回值含义如下：

- `true`：事件已经进入 SDK 本地队列；
- `false`：事件非法、队列已满或客户端已经关闭，同时触发 `OnDrop`；
- 后台 HTTP 发送失败不会回写已经返回的调用栈，而是异步触发 `OnDrop`。

## 5. 在应用关闭时排空

停止接收业务请求后，应调用 `Client.Close`。关闭会拒绝新事件，并等待队列中的批次完成发送：

```go
func Shutdown(runtime *Runtime) {
    shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    if err := runtime.Client.Close(shutdownCtx); err != nil {
        log.Printf("logging client drain failed: %v", err)
    }
}
```

建议顺序是：停止新请求、停止产生新日志的 worker、关闭日志客户端、退出进程。不要使用已经被应用取消的 request context 进行全局排空。`Close(ctx)` 到期后会取消正在进行的 HTTP 请求和 `Retry-After` 等待，不再开始新的尝试；未发送事件会逐条进入 `OnDrop`，错误链可以通过 `errors.Is(err, logging.ErrClientClosed)` 识别。

`Emitter.Emit` 的 context 供 Handler、Logger 和 trace provider 在入队前提取调用方上下文；事件一旦进入异步队列，就由客户端生命周期管理，不继续继承业务请求的取消信号。

## 6. 常用客户端参数

未填写的限制使用 SDK 默认值：

| 字段 | 默认值 | 说明 |
| --- | --- | --- |
| `Timeout` | `7s` | 单次 HTTP 请求超时 |
| `QueueSize` | `4096` | 本地事件队列容量 |
| `QueueBytes` | `16MiB` | 尚未完成发送的规范化事件累计字节上限 |
| `BatchSize` | `128` | 单次发送目标事件数 |
| `FlushInterval` | `100ms` | 未达到批量大小时的刷新周期 |
| `MaxBodyBytes` | `1MiB` | 单次 HTTP body 上限，包含 batch envelope；超限批次会继续拆分 |
| `MaxAttempts` | `3` | 单批总尝试次数，包含首次请求 |
| `InitialBackoff` | `100ms` | 首次重试的最大抖动退避 |
| `MaxBackoff` | `1s` | 指数退避上限 |
| `MaxRetryAfter` | `30s` | 服务端 `Retry-After` 等待上限，最大允许 `1h` |
| `HTTPClient` | SDK 创建 | 注入自定义 `http.Client`，通常用于测试或统一 transport |
| `OnDrop` | 空 | 事件无法入队或发送时的 callback |
| `FallbackWriter` | `os.Stderr` | callback 缺失或 panic 时的限频降级输出 |

构造函数会立即拒绝非法 URL、空 token、冲突退避、负数限制和异常大的容量配置。队列最多 `1000000` 条或 `1GiB`，批次最多 `10000` 条，尝试次数最多 `10`，时间参数最多一小时；容量类字段填 `0` 表示使用默认值。SDK 只重试网络异常和 `408`、`425`、`429`、`500`、`502`、`503`、`504`；其他 4xx 与格式异常的成功响应不会重试。合法的 `Retry-After` 可以是整数秒或 HTTP-date，实际等待取本地抖动退避与服务端要求的较大值，再受 `MaxRetryAfter` 限制；非法、负数和已过期值会被忽略。请求结果不确定时可能已经被服务端接受，重试复用相同 `event_id`，下游仍须允许重复。

默认 `7s` 是按服务端默认 `500ms` 聚合等待和 `5s` Kafka 发布超时预留的客户端预算。如果部署提高 `STELLARMESH_LOGGING_BATCH_FLUSH_INTERVAL` 或 `STELLARMESH_LOGGING_KAFKA_PUBLISH_TIMEOUT`，必须同步提高客户端 `Timeout`，否则客户端可能在服务端完成持久确认前超时并重试。

## 7. 接入验证

在业务测试环境完成以下检查：

1. 启动业务进程后写入一条带唯一 `order_id` 或测试标识的 `INFO` 日志；
2. 确认 logger 返回 `true`，且 `OnDrop` 没有收到错误；
3. 确认 `logging-service` 的 `/health/ready` 为 `200`；
4. 在 ClickHouse 中按 `service`、测试标识或 `event_id` 找到事件；
5. 停止业务进程，确认 `Client.Close` 没有超时；
6. 在故障演练中让 `logging-service` 返回错误，确认业务请求不被日志失败中断，drop 指标能够告警。

SDK 调用成功只表示本地入队；`logging-service` 返回 `202` 表示 Kafka 或持久 spool 已确认；ClickHouse 最终可查询是第三个独立检查点。

## 8. 升级注意事项

- 固定 SDK tag，不要引用 `main`、`dev` 或未固定 commit；
- 升级前阅读发布说明，并在测试环境执行 `go test ./...`；
- `service` 改名必须同步更新 token 绑定和查询口径，不应只改业务代码；
- 如果调整队列或批量大小，应同时观察 drop 计数、关闭排空时间和请求 body 上限；
- Go SDK、Python SDK 与服务镜像应尽量来自同一发布 commit，避免契约漂移。

## 9. 网关项目

网关 SDK 与日志客户端位于同一个 Go module，但生命周期不同：网关 SDK 返回普通 `http.Handler`，不创建监听端口、不读取环境变量，也不拥有业务路由；日志客户端拥有异步发送 worker，需要在进程退出前关闭。业务项目应在自己的 `main.go` 中组装二者，并继续使用项目配置层管理 upstream 地址、JWT Secret、Redis 地址和 CORS origin。

网关的最小接入、完整 `WithXxx` 列表、固定执行顺序和 fail-close 语义见[Go 网关 SDK 接入教程](gateway.md)。

## 10. 对象存储项目

`objectstorage` 定义 provider-neutral 小接口、namespace/key 校验、公共错误与 Observer；`objectstorage/s3store` 使用 AWS SDK for Go v2 实现 AWS S3 和 S3-compatible 适配。每个客户端在构造时固定 Bucket 与 Prefix，业务请求只传逻辑 key。

Go 服务可以直接流式 Put/Get、读取 Stat、删除指定版本、生成预签名请求或显式管理 Multipart。SDK 不创建临时文件，不自动创建 Bucket，也不提供 List、Copy、ACL、CORS、Policy、Lifecycle 和 Versioning 管理。完整构造方式、MinIO 双 Endpoint、Reader 关闭责任、错误语义与权限边界见[Go 对象存储 SDK 接入教程](object-storage.md)。
