# Gateway 扩展配置

[返回接入入口](gateway.md)。场景代码为装配片段；[完整可执行示例](../../../sdk/go/gateway/example_test.go)演示本地代理与资源关闭。

## 路由和 upstream

`WithRoutes` 支持 HTTP Method、精确路径和路径前缀。解析时先匹配精确路径，再匹配最长前缀；Method 为空表示接受所有方法。重复条件、非法路径、负数请求体上限或不存在的 upstream 会让构造失败。

`WithUpstreams` 在启动阶段解析全部 URL，并为每个 upstream 构造一个可复用的 `httputil.ReverseProxy`。代理使用 `Rewrite` 重建转发头，不继承客户端伪造的 `Forwarded` 或 `X-Forwarded-*`。连接失败返回 `502`，超时返回 `504`，超过路由 `MaxBodyBytes` 返回 `413`。SDK 不自动重试请求，避免重复执行 `POST` 等非幂等操作。

代理不缓冲正常请求体和响应体，并保留 Flush 与 Hijack 能力，可以用于 SSE、流式响应和 WebSocket。长连接项目不能照搬普通 API 服务的全局 `WriteTimeout`；应根据最长流式请求显式设置，必要时使用零值并依赖 upstream、连接和基础设施层超时控制。

项目如果使用动态路由或自定义代理，可以分别传入 `WithRouteResolver` 和 `WithUpstreamResolver`。它们与对应的静态组件互斥；动态解析出的路由仍会在请求期校验名称、upstream、访问模式和请求体限制，错误返回 `503`。

## 请求 ID

从 `v0.5.0` 起，网关默认忽略并移除传入的 `X-Request-ID`，为每个请求重新生成 ID；省略 `WithRequestID` 与 `TrustIncoming: false` 相同。网关选定的值写入转发请求头、请求上下文和访问日志，并设置响应头，便于客户端关联排障。

主干已补齐内置反向代理的一致性处理（尚未包含在已发布的 `v0.5.0`）：逐跳请求头清理后，从上下文恢复选定 ID，避免客户端通过 `Connection: X-Request-ID` 让转发头丢失；复制后端响应头前移除后端同名字段，客户端响应头只保留网关选定的一个值。该处理使用 `RequestIDConfig.Header` 配置的名称，不重新调用生成器，也不改变 `TrustIncoming` 的入口选择规则。自定义 `WithUpstreamResolver` 返回的处理器需自行遵守相同的传递与响应约定。

仅在入口受控、前置代理会覆盖客户端原始 ID 时显式允许沿用：

```go
gateway.WithRequestID(gateway.RequestIDConfig{
    TrustIncoming: true,
})
```

`TrustIncoming` 不验证请求来源，也不与 `WithTrustedProxies` 联动。后者只控制客户端 IP 转发头的信任；若前置代理原样透传客户端 ID，开启此项仍会采纳客户端提供的值。

启用后只接受单个合法值：沿用现有首尾空白去除规则，内容须为可见 ASCII，默认最多 128 字节。缺失、空值、非法字符、超长以及重复请求头（即使内容相同）均重新生成，不因此拒绝整个请求。`Header` 可指定其他 HTTP 头，所有读取、替换及长度检查均针对该配置头；`MaxLength` 为 0 使用 128，显式范围为 16～1024。

`Generate` 可注入项目生成器；默认使用 16 字节密码学随机值的十六进制文本。自定义生成结果仍需通过相同长度和字符校验。生成失败或结果非法时返回 `503 request_id_unavailable`，不转发请求，也不会退回传入值。Request-ID 用于关联单次请求，不充当身份凭据或幂等键。

## 身份和策略扩展

`jwtauth` 首版只提供 HS256：Secret 至少 `32` 字节，算法固定为 HS256，必须配置 issuer 和 audience，token 必须包含有效 expiration 与非空 subject，默认允许 `30s` 时钟偏差。默认 Claims 使用 `sub` 作为 `UserID` 并读取字符串数组 `roles`；特殊 Claims 可以通过 `ClaimsFactory` 和 `IdentityMapper` 映射。其他算法或 JWKS 应由项目实现 `gateway.Authenticator` 后通过 `WithAuthenticator` 注入。

正常的无效或过期 token 返回 `401`。Authenticator 自身返回错误表示认证依赖故障，流水线返回 `503`。认证成功后，SDK 无条件删除请求中已有的 `X-User-ID` 和 `X-User-Roles`，再写入可信身份，防止客户端伪造服务间身份头。

项目授权实现 `Authorizer`，最后的业务转发检查实现 `BeforeProxyPolicy`。策略正常拒绝返回 `403`，策略执行错误返回 `503`。SDK 不开放任意阶段 Middleware，也不允许通过 Option 顺序把项目逻辑插入身份头清理之后。

## 客户端地址和 CORS

默认客户端地址只来自 `RemoteAddr`。`WithTrustedProxies` 接受一个或多个 CIDR；只有直接对端位于这些 CIDR 时才读取 `X-Forwarded-For` 或 `X-Real-IP`，并从右向左跳过可信代理。可信代理发送非法转发链时返回 `503`，不会退回可伪造的地址。

CORS 未声明时不处理跨域。启用后必须显式提供 origin；method 和 header 省略时使用 SDK 的有限默认集合。未知 origin、method 或预检 header 返回 `403`，不会把 `Access-Control-Request-Headers` 原样回显。`AllowCredentials` 默认关闭，通配 origin 不能与凭据同时使用。

## 错误、日志和观测

默认错误响应使用纯文本；项目可以通过 `WithErrorResponder` 把稳定错误代码映射到自己的 JSON 字段。状态和错误分类如下：

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

### 默认标准库访问日志

Gateway `v0.3.0` 默认通过请求完成时读取的 `slog.Default()` 输出一条 `gateway request completed`。Go 默认 Logger 写到进程 `stderr`，因此无需 Sink 或远程服务即可在 CLI 查看。项目可以统一替换默认 Logger：

```go
slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
    Level: slog.LevelInfo,
})))

handler, err := gateway.New(
    gateway.WithRoutes(routes...),
    gateway.WithUpstreams(upstreams...),
)
```

也可以给 Gateway 使用独立 Logger，或显式关闭：

```go
gateway.WithSlogAccessLogger(gateway.SlogAccessLoggerConfig{
    Logger: projectLogger,
    IncludeIdentity: false,
})

gateway.WithoutAccessLog()
```

小于 `400` 的状态使用 `INFO`，`4xx` 使用 `WARN`，`5xx` 使用 `ERROR`。默认字段包括请求 ID、method、path、路由名、可信解析后的客户端 IP、鉴权结果、upstream、状态、耗时、错误代码和限流结果；默认不包含 QueryString、请求头、Cookie、请求体、响应体、用户 ID 或角色。只有显式设置 `IncludeIdentity` 才加入用户身份。成功的本地健康探针默认跳过，可通过 `HealthConfig.LogSuccessful` 开启。

`WithAccessLogger` 继续允许项目注入自己的通用实现。三种配置入口占用同一组件槽位，不能同时声明，避免 Option 顺序决定日志行为。

`WithObserver` 接收请求完成、决策组件故障和访问日志失败三类低基数事件。Observer 和 AccessLogger 的错误或 panic 都不会改变业务响应。项目可以在 Observer 外部适配 Prometheus，标签只应使用路由名、upstream、状态和固定组件名，不应使用 path、用户 ID、请求 ID 或原始错误文本。

## 健康检查和测试

`WithHealth` 默认提供 `GET /health/live` 和 `GET /health/ready`。存活检查只表示进程能够响应；就绪检查可以注入 `ReadinessChecker`，默认超时为 `2s`，错误或超时返回 `503`。健康路径由网关本地处理，不需要出现在业务路由表中。成功时默认返回 `ok`；项目响应器通过 `HealthKindLive` 和 `HealthKindReady` 区分端点，通过 `HealthResult.Service` 读取规范化后的服务名。失败仍统一交给 `ErrorResponder`。

业务项目接入时至少应验证：

1. 公开路由无需 token，受保护路由缺少或伪造 token 时不能到达 upstream；
2. 客户端伪造身份头和转发头不会被信任；
3. Redis 故障、授权器错误和动态路由错误都返回 `503`；
4. CORS 预检不会进入鉴权和 upstream；
5. SSE、流式响应和 WebSocket 不被响应包装器破坏；
6. 访问日志失败不会改变上游已经产生的状态码；
7. `/health/ready` 能反映项目声明的关键依赖状态。

