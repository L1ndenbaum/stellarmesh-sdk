# Go SDK 接入教程

本教程对应 Go module `github.com/L1ndenbaum/stellarmesh-sdk/sdk/go`，适用于 Go 1.22 及以上版本。业务日志接入只需要导入其中的 `logging` package。

## 1. 安装固定版本

正式接入应使用 `sdk/go/vX.Y.Z` 子模块 tag：

```sh
go get github.com/L1ndenbaum/stellarmesh-sdk/sdk/go@v0.1.0
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
- `Service` 是当前进程发送日志时使用的稳定身份，必须与 token 的授权绑定一致。

## 3. 创建进程级客户端和 logger

一个业务进程通常只创建一个 `Client`。HTTP handler、后台 worker 和定时任务可以共享它，不要为每个请求创建新的后台发送线程。

```go
package applogging

import (
    "context"
    "fmt"
    "io"
    "log"
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
    Logger *logging.Logger
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

    logger, err := logging.NewLogger(logging.LoggerConfig{
        Service: config.Service,
        Emitter: client,
        TraceIDProvider: func(ctx context.Context) string {
            return TraceIDFromContext(ctx)
        },
    })
    if err != nil {
        closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
        defer cancel()
        _ = client.Close(closeCtx)
        return nil, fmt.Errorf("create logging logger: %w", err)
    }
    return &Runtime{Client: client, Logger: logger}, nil
}

func TraceIDFromContext(context.Context) string {
    return ""
}
```

示例中的 `TraceIDFromContext` 是业务适配边界。SDK 不依赖业务请求上下文或 OpenTelemetry；如果项目已有 trace，应在这里从调用者传入的 `context.Context` 中提取。

`OnDrop` 必须轻量、线程安全，并且不能再次调用同一个远端 logger，否则网络失败时可能递归。没有配置 `OnDrop` 时，SDK 会通过 `FallbackWriter` 限频输出降级警告；如果两者都未配置，则使用标准错误输出。

## 4. 写入结构化日志

logger 提供 `Debug`、`Info`、`Warning`、`Error` 和 `Audit`：

```go
func HandleOrder(ctx context.Context, logger *logging.Logger, orderID string) {
    accepted := logger.Info(
        ctx,
        "order created",
        "",
        map[string]any{
            "order_id": orderID,
            "component": "order-handler",
        },
    )
    if !accepted {
        // 这里只记录指标或执行轻量降级，不应让日志失败破坏业务请求。
    }
}
```

第三个参数是显式 `traceID`。非空时优先使用该值；为空时才调用 `TraceIDProvider`。metadata 会经过深度、序列长度、字符串长度和敏感键处理，常见的 token、password、secret、authorization 等字段会被脱敏，但业务代码仍不应主动把 Secret 放入日志。

返回值含义如下：

- `true`：事件已经进入 SDK 本地队列；
- `false`：事件非法、队列已满或客户端已经关闭，同时触发 `OnDrop`；
- 后台 HTTP 发送失败不会回写已经返回的调用栈，而是异步触发 `OnDrop`。

## 5. 在应用关闭时排空

停止接收业务请求后，应调用 `Client.Close`。关闭会拒绝新事件，并等待队列中的批次完成发送：

```go
func Shutdown(runtime *Runtime) {
    shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
    defer cancel()
    if err := runtime.Client.Close(shutdownCtx); err != nil {
        log.Printf("logging client drain failed: %v", err)
    }
}
```

建议顺序是：停止新请求、停止产生新日志的 worker、关闭日志客户端、退出进程。不要使用已经被应用取消的 request context 进行全局排空。

## 6. 常用客户端参数

未填写的限制使用 SDK 默认值：

| 字段 | 默认值 | 说明 |
| --- | --- | --- |
| `Timeout` | `2s` | 单次 HTTP 请求超时 |
| `QueueSize` | `4096` | 本地事件队列容量 |
| `QueueBytes` | `16MiB` | 尚未完成发送的规范化事件累计字节上限 |
| `BatchSize` | `128` | 单次发送目标事件数 |
| `FlushInterval` | `100ms` | 未达到批量大小时的刷新周期 |
| `MaxBodyBytes` | `900KiB` | 单次 HTTP body 上限，超限批次会继续拆分 |
| `MaxAttempts` | `3` | 单批总尝试次数，包含首次请求 |
| `InitialBackoff` | `100ms` | 首次重试的最大抖动退避 |
| `MaxBackoff` | `1s` | 指数退避上限 |
| `HTTPClient` | SDK 创建 | 注入自定义 `http.Client`，通常用于测试或统一 transport |
| `OnDrop` | 空 | 事件无法入队或发送时的 callback |
| `FallbackWriter` | `os.Stderr` | callback 缺失或 panic 时的限频降级输出 |

构造函数会立即拒绝非法 URL、空 token、冲突退避、负数限制和异常大的容量配置。队列最多 `1000000` 条或 `1GiB`，批次最多 `10000` 条，尝试次数最多 `10`，时间参数最多一小时；容量类字段填 `0` 表示使用默认值。SDK 只重试网络异常和 `408`、`425`、`429`、`500`、`502`、`503`、`504`；其他 4xx 与格式异常的成功响应不会重试。请求结果不确定时可能已经被服务端接受，重试复用相同 `event_id`，下游仍须允许重复。

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
