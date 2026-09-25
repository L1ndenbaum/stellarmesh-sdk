# Gateway 首次装配

[返回接入入口](gateway.md)。场景代码为装配片段；[完整可执行示例](../../../sdk/go/gateway/example_test.go)演示本地代理与资源关闭。

## 安装固定版本

只使用网关能力的项目直接安装独立 Module：

```sh
go get github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/gateway@v0.3.1
go mod tidy
```

本文安装示例适用于 `v0.3.1`，当前发布状态见[发布矩阵](../../release.md#当前制品矩阵)。该版本同时包含基础 Gateway、`gateway/jwtauth` 和 `gateway/redislimit`，只直接依赖 JWT 和 Redis，不引入父 SDK、Logging、AWS SDK、对象存储、Chi 或 Kafka。

如果项目还使用父 SDK，建议固定当前已经移除所有嵌套能力的 `v0.5.0`：

```sh
go get \
  github.com/L1ndenbaum/stellarmesh-sdk/sdk/go@v0.5.0 \
  github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/gateway@v0.3.1
go mod tidy
```

`sdk/go/v0.3.0` 仍不可变地包含旧 Gateway package，不能与独立 Gateway Module 同时进入一个 build list，否则可能产生 `ambiguous import`。业务仓库不应通过长期 `replace` 绕过这一版本边界。

## 设计语义

项目通过 `gateway.New(options ...Option)` 和 `WithXxx` 声明组件。Option 的书写顺序不影响请求执行顺序，SDK 固定执行以下流程：

```text
恢复与响应记录
  -> 请求 ID 和访问日志上下文
  -> CORS 和本地健康端点
  -> 路由解析
  -> 可信客户端 IP
  -> 客户端 IP 限流
  -> Bearer 认证
  -> 用户限流
  -> 项目授权
  -> 转发前项目策略
  -> upstream 限流
  -> 清理并注入可信身份头
  -> 反向代理
  -> 访问日志和 Observer
```

未声明可选组件表示不启用该能力。组件一旦声明，路由、客户端地址、鉴权、授权、限流、转发策略或 upstream 解析发生错误时都停止转发。访问日志和 Observer 属于旁路能力，其失败不会改变业务响应。

静态路由的零值 `Access` 是 `AccessProtected`。公开接口必须显式设置 `AccessPublic`；未匹配路径返回 `404`，不会隐式转发到默认后端。静态受保护路由没有 Authenticator 时，`gateway.New` 直接返回错误。

## 响应协议归项目所有

Gateway 只确定 HTTP 状态、稳定错误代码、通用错误消息、`Retry-After` 和健康检查结果，不规定业务 JSON Schema。`v0.3.0` 的默认错误响应是 `text/plain; charset=utf-8`，正文为通用错误消息；健康检查成功同样返回纯文本 `ok`。默认响应不会包含 `error_reason`、时间戳或 Stellarmesh `ApiEnvelope`。

项目需要统一 JSON 时，在业务仓库实现 `ErrorResponder` 和 `HealthResponder`。下面只是项目自己的协议示例，不属于 SDK 契约：

```go
type apiEnvelope struct {
    Code        int    `json:"code"`
    Message     string `json:"message"`
    Data        any    `json:"data"`
    ErrorReason string `json:"error_reason,omitempty"`
}

func writeProjectJSON(w http.ResponseWriter, status int, envelope apiEnvelope) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(status)
    _ = json.NewEncoder(w).Encode(envelope)
}

var projectErrorResponder = gateway.ErrorResponderFunc(func(
    w http.ResponseWriter,
    _ *http.Request,
    gatewayError gateway.GatewayError,
) {
    writeProjectJSON(w, gatewayError.Status, apiEnvelope{
        Code:        gatewayError.Status,
        Message:     gatewayError.Message,
        ErrorReason: gatewayError.Code,
    })
})

var projectHealthResponder = gateway.HealthResponderFunc(func(
    w http.ResponseWriter,
    _ *http.Request,
    result gateway.HealthResult,
) {
    writeProjectJSON(w, http.StatusOK, apiEnvelope{
        Code:    http.StatusOK,
        Message: "操作成功",
        Data: map[string]string{
            "status":  "ok",
            "service": result.Service,
            "kind":    string(result.Kind),
        },
    })
})
```

Gateway 会在调用项目错误响应器前设置 `Retry-After`。`GatewayError.Cause` 只用于内部诊断，不能序列化给客户端；如果项目响应器在写入响应前 panic，SDK 会使用中立的 `500 internal server error` 兜底。响应器开始写入后无法撤回已经发送的状态和正文，因此实现应保持简单、无外部 I/O，并在业务仓库中单独测试。

## 完整组装示例

下面的函数展示一个项目如何从自己的配置层注入依赖，并沿用上一节由业务仓库定义的两个响应器。真实地址、Secret、速率和 origin 仍由业务项目管理。

```go
package appgateway

import (
    "context"
    "fmt"
    "net/http"
    "time"

    "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/gateway"
    "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/gateway/jwtauth"
    "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/gateway/redislimit"
    "github.com/redis/go-redis/v9"
)

type Config struct {
    BackendURL    string
    JWTSecret     []byte
    JWTIssuer     string
    JWTAudience   string
    AllowedOrigin string
    TrustedProxy  string
    RateKeyPrefix string
}

func NewHandler(
    config Config,
    redisClient *redis.Client,
) (http.Handler, error) {
    authenticator, err := jwtauth.New(jwtauth.Config{
        Secret:   config.JWTSecret,
        Issuer:   config.JWTIssuer,
        Audience: config.JWTAudience,
    })
    if err != nil {
        return nil, fmt.Errorf("创建 JWT 认证器: %w", err)
    }

    clientLimiter, err := redislimit.New(redislimit.Config{
        Client: redisClient, Scope: gateway.RateLimitScopeClientIP,
        KeyPrefix: config.RateKeyPrefix, RatePerSecond: 20, Burst: 40,
    })
    if err != nil {
        return nil, fmt.Errorf("创建客户端限流器: %w", err)
    }
    userLimiter, err := redislimit.New(redislimit.Config{
        Client: redisClient, Scope: gateway.RateLimitScopeUserID,
        KeyPrefix: config.RateKeyPrefix, RatePerSecond: 10, Burst: 20,
    })
    if err != nil {
        return nil, fmt.Errorf("创建用户限流器: %w", err)
    }
    upstreamLimiter, err := redislimit.New(redislimit.Config{
        Client: redisClient, Scope: gateway.RateLimitScopeUpstream,
        KeyPrefix: config.RateKeyPrefix, RatePerSecond: 100, Burst: 200,
    })
    if err != nil {
        return nil, fmt.Errorf("创建上游限流器: %w", err)
    }

    return gateway.New(
        gateway.WithRoutes(
            gateway.Route{
                Name: "login",
                Match: gateway.RouteMatch{
                    Methods: []string{http.MethodPost},
                    ExactPath: "/api/v1/auth/login",
                },
                Upstream: "backend",
                Access: gateway.AccessPublic,
                MaxBodyBytes: 1 << 20,
            },
            gateway.Route{
                Name: "backend",
                Match: gateway.RouteMatch{PathPrefix: "/"},
                Upstream: "backend",
                MaxBodyBytes: 32 << 20,
            },
        ),
        gateway.WithUpstreams(
            gateway.Upstream{Name: "backend", URL: config.BackendURL},
        ),
        gateway.WithTrustedProxies(config.TrustedProxy),
        gateway.WithCORS(gateway.CORSConfig{
            AllowedOrigins: []string{config.AllowedOrigin},
            AllowedMethods: []string{
                http.MethodGet, http.MethodPost, http.MethodPut,
                http.MethodPatch, http.MethodDelete, http.MethodOptions,
            },
            AllowedHeaders: []string{"Authorization", "Content-Type", "X-Request-ID"},
            AllowCredentials: true,
            MaxAge: 10 * time.Minute,
        }),
        gateway.WithAuthenticator(authenticator),
        gateway.WithClientIPRateLimiter(clientLimiter),
        gateway.WithUserRateLimiter(userLimiter),
        gateway.WithUpstreamRateLimiter(upstreamLimiter),
        gateway.WithErrorResponder(projectErrorResponder),
        gateway.WithHealth(gateway.HealthConfig{
            Service: "example-gateway",
            Responder: projectHealthResponder,
            Readiness: gateway.ReadinessCheckerFunc(func(ctx context.Context) error {
                return redisClient.Ping(ctx).Err()
            }),
        }),
    )
}
```

上面的组装代码在 `v0.3.0` 中省略日志组件时使用 `slog.Default()`。项目从 `v0.2.0` 升级前应把默认访问日志带来的输出量纳入容量和日志等级评估；不需要访问日志时显式使用 `WithoutAccessLog()`。

`redislimit` 的 `RatePerSecond`、`Burst` 和 `KeyPrefix` 必须显式且大于零。禁用某个限流维度时，不要构造一个零速率 limiter，而是省略对应的 `With...RateLimiter`。

## 原生可验证示例

[完整示例](../../../sdk/go/gateway/example_test.go)通过公开 import 使用组件。本例启动并关闭两个本地测试服务器。真实部署需自行配置安全策略；Session 与凭证提取扩展要求 Gateway `v0.4.0` 或更新版本。
