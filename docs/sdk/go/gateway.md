# Go 网关 SDK 接入教程

网关 SDK 位于 `github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/gateway`。它返回标准 `http.Handler`，业务项目继续拥有 `main.go`、环境变量解析、路由表、upstream 地址和部署配置。SDK 不启动监听端口，也不提供可直接部署的公共 gateway 进程。

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

## 完整组装示例

下面的函数展示一个项目如何从自己的配置层注入依赖。真实地址、Secret、速率和 origin 仍由业务项目管理。

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
    sharedlogging "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging"
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
    logEmitter sharedlogging.Emitter,
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
        gateway.WithAccessLogEmitter("example-gateway", logEmitter),
        gateway.WithHealth(gateway.HealthConfig{
            Service: "example-gateway",
            Readiness: gateway.ReadinessCheckerFunc(func(ctx context.Context) error {
                return redisClient.Ping(ctx).Err()
            }),
        }),
    )
}
```

`redislimit` 的 `RatePerSecond`、`Burst` 和 `KeyPrefix` 必须显式且大于零。禁用某个限流维度时，不要构造一个零速率 limiter，而是省略对应的 `With...RateLimiter`。

## 路由和 upstream

`WithRoutes` 支持 HTTP Method、精确路径和路径前缀。解析时先匹配精确路径，再匹配最长前缀；Method 为空表示接受所有方法。重复条件、非法路径、负数请求体上限或不存在的 upstream 会让构造失败。

`WithUpstreams` 在启动阶段解析全部 URL，并为每个 upstream 构造一个可复用的 `httputil.ReverseProxy`。代理使用 `Rewrite` 重建转发头，不继承客户端伪造的 `Forwarded` 或 `X-Forwarded-*`。连接失败返回 `502`，超时返回 `504`，超过路由 `MaxBodyBytes` 返回 `413`。SDK 不自动重试请求，避免重复执行 `POST` 等非幂等操作。

代理不缓冲正常请求体和响应体，并保留 Flush 与 Hijack 能力，可以用于 SSE、流式响应和 WebSocket。长连接项目不能照搬普通 API 服务的全局 `WriteTimeout`；应根据最长流式请求显式设置，必要时使用零值并依赖 upstream、连接和基础设施层超时控制。

项目如果使用动态路由或自定义代理，可以分别传入 `WithRouteResolver` 和 `WithUpstreamResolver`。它们与对应的静态组件互斥；动态解析出的路由仍会在请求期校验名称、upstream、访问模式和请求体限制，错误返回 `503`。

## 身份和策略扩展

`jwtauth` 首版只提供 HS256：Secret 至少 `32` 字节，算法固定为 HS256，必须配置 issuer 和 audience，token 必须包含有效 expiration 与非空 subject，默认允许 `30s` 时钟偏差。默认 Claims 使用 `sub` 作为 `UserID` 并读取字符串数组 `roles`；特殊 Claims 可以通过 `ClaimsFactory` 和 `IdentityMapper` 映射。其他算法或 JWKS 应由项目实现 `gateway.Authenticator` 后通过 `WithAuthenticator` 注入。

正常的无效或过期 token 返回 `401`。Authenticator 自身返回错误表示认证依赖故障，流水线返回 `503`。认证成功后，SDK 无条件删除请求中已有的 `X-User-ID` 和 `X-User-Roles`，再写入可信身份，防止客户端伪造服务间身份头。

项目授权实现 `Authorizer`，最后的业务转发检查实现 `BeforeProxyPolicy`。策略正常拒绝返回 `403`，策略执行错误返回 `503`。SDK 不开放任意阶段 Middleware，也不允许通过 Option 顺序把项目逻辑插入身份头清理之后。

## 客户端地址和 CORS

默认客户端地址只来自 `RemoteAddr`。`WithTrustedProxies` 接受一个或多个 CIDR；只有直接对端位于这些 CIDR 时才读取 `X-Forwarded-For` 或 `X-Real-IP`，并从右向左跳过可信代理。可信代理发送非法转发链时返回 `503`，不会退回可伪造的地址。

CORS 未声明时不处理跨域。启用后必须显式提供 origin；method 和 header 省略时使用 SDK 的有限默认集合。未知 origin、method 或预检 header 返回 `403`，不会把 `Access-Control-Request-Headers` 原样回显。`AllowCredentials` 默认关闭，通配 origin 不能与凭据同时使用。

## 错误、日志和观测

默认错误响应使用共享 `Envelope`，HTTP 状态同时写入 `code`，稳定错误标识写入 `error_reason`。主要行为如下：

| 场景 | 状态 |
| --- | --- |
| 未匹配路由 | `404` |
| 缺少或无效 Bearer token | `401` |
| 授权或转发策略拒绝 | `403` |
| CORS 拒绝 | `403` |
| 请求超过路由上限 | `413` |
| 正常限流拒绝 | `429`，并在可用时写入 `Retry-After` |
| 安全决策组件错误 | `503` |
| upstream 连接失败 | `502` |
| upstream 超时 | `504` |

`WithAccessLogEmitter` 直接调用现有 `logging.Emitter`，不会创建第二个队列或 worker。访问事件包含请求 ID、method、path、路由名、客户端 IP、鉴权与限流结果、upstream、状态和耗时；默认不记录 QueryString、Authorization 或身份凭据。成功的本地健康探针默认不写访问日志，可以通过 `HealthConfig.LogSuccessful` 开启。

`WithObserver` 接收请求完成、决策组件故障和访问日志失败三类低基数事件。Observer 和 AccessLogger 的错误或 panic 都不会改变业务响应。项目可以在 Observer 外部适配 Prometheus，标签只应使用路由名、upstream、状态和固定组件名，不应使用 path、用户 ID、请求 ID 或原始错误文本。

## 健康检查和测试

`WithHealth` 默认提供 `GET /health/live` 和 `GET /health/ready`。存活检查只表示进程能够响应；就绪检查可以注入 `ReadinessChecker`，默认超时为 `2s`，错误或超时返回 `503`。健康路径由网关本地处理，不需要出现在业务路由表中。

业务项目接入时至少应验证：

1. 公开路由无需 token，受保护路由缺少或伪造 token 时不能到达 upstream；
2. 客户端伪造身份头和转发头不会被信任；
3. Redis 故障、授权器错误和动态路由错误都返回 `503`；
4. CORS 预检不会进入鉴权和 upstream；
5. SSE、流式响应和 WebSocket 不被响应包装器破坏；
6. 访问日志失败不会改变上游已经产生的状态码；
7. `/health/ready` 能反映项目声明的关键依赖状态。

本次 SDK 不包含共享 gateway 可执行程序，也不包含服务发现、动态配置、配置热更新、自动重试、熔断、WAF、缓存、灰度路由或管理控制面。这些能力应在出现明确的跨项目需求后独立设计。
