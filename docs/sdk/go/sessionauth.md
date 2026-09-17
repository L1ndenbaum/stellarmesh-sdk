# Redis Session 认证与会话管理

`gateway/sessionauth` 是网关 Module 中与 `jwtauth` 并列的包。本文描述主干新增、尚未发布的接口，不应将现有发布版本当作已经包含这些能力。

## 依赖与职责

使用本包必须提供 Redis 服务；Go 客户端使用网关 Module 已有的 `github.com/redis/go-redis/v9`。SDK 不负责 Redis 安装、部署、连接配置或 Client 关闭。业务方可以使用单实例 `redis.NewClient` 或 Sentinel 的 `redis.NewFailoverClient`，并将返回的 `*redis.Client` 注入 Store。

初版不支持 Redis Cluster：会话记录与用户索引由同一主节点上的原子脚本维护，多 key 没有使用 hash tag。显式的 `*redis.ClusterClient` 会被构造器拒绝，也不能通过包装器绕过此限制。Redis Client 的超时、重试与连接池由业务方配置；写入响应丢失时，客户端不能据此确认写入是否发生，SDK 不提供跨网络故障的恰好一次保证。

业务方负责登录校验、授权、Cookie 设置、CSRF 防护、CORS 和管理员踢人权限。本包不注册 HTTP 路由、不安装 Redis、不启动后台清理任务、不在认证时自动续期。

## 装配 Cookie Session

下面的装配函数接收业务方创建的 Client 和上游处理器。登录、刷新、退出由上游处理；业务方需要对这些端点实现自己的校验和 CSRF 策略。公开路由只表示网关不要求已有会话，不代表业务端点可以无条件执行操作。

```go
package appgateway

import (
    "net/http"

    "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/gateway"
    "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/gateway/sessionauth"
    "github.com/redis/go-redis/v9"
)

func NewGateway(client *redis.Client, upstream http.Handler) (
    http.Handler, *sessionauth.Store, error,
) {
    store, err := sessionauth.NewStore(sessionauth.StoreConfig{
        Client:             client,
        ProjectScope:       "kgraph",
        KeySeparator:       ":",
        MaxSessionsPerUser: 10,
    })
    if err != nil {
        return nil, nil, err
    }
    authenticator, err := sessionauth.NewAuthenticator(store)
    if err != nil {
        return nil, nil, err
    }
    extract, err := gateway.CookieCredential("session_id")
    if err != nil {
        return nil, nil, err
    }
    handler, err := gateway.New(
        gateway.WithRoutes(
            gateway.Route{
                Name: "login", Match: gateway.RouteMatch{ExactPath: "/auth/login"},
                Upstream: "backend", Access: gateway.AccessPublic,
            },
            gateway.Route{
                Name: "refresh", Match: gateway.RouteMatch{ExactPath: "/auth/refresh"},
                Upstream: "backend", Access: gateway.AccessPublic,
            },
            gateway.Route{
                Name: "logout", Match: gateway.RouteMatch{ExactPath: "/auth/logout"},
                Upstream: "backend", Access: gateway.AccessPublic,
            },
            gateway.Route{
                Name: "api", Match: gateway.RouteMatch{PathPrefix: "/api/"},
                Upstream: "backend",
            },
        ),
        gateway.WithUpstreamResolver(gateway.UpstreamResolverFunc(
            func(gateway.Route) (http.Handler, error) { return upstream, nil },
        )),
        gateway.WithAuthenticator(authenticator, extract),
    )
    if err != nil {
        return nil, nil, err
    }
    return handler, store, nil
}
```

分进程部署时，登录服务与网关分别创建 Store，但项目 scope、分隔符和最大会话数必须保持一致。身份从会话读取后继续使用网关现有的身份头注入、用户限流及授权流程。

`gateway.BearerCredential()` 可以替换 Cookie 提取器；也可以传入 `func(*http.Request) (string, error)`。提取器只选择一种来源，不隐式回退。已有 JWT 的 `WithAuthenticator(jwtAuthenticator)` 继续默认提取 Bearer。

## 登录创建、显式续期与退出

以下函数放在业务 HTTP 层。`CompleteLogin` 只接收业务方已经验证的身份，每次成功登录创建新的随机 Session ID，不接受请求传入的 ID。`Refresh` 和 `Logout` 应注册为 POST，并在进入函数前完成业务方的 CSRF 校验；下例不代替这层校验。

```go
package appgateway

import (
    "errors"
    "net/http"
    "time"

    "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/gateway"
    "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/gateway/sessionauth"
)

const sessionTTL = 24 * time.Hour

func setSessionCookie(w http.ResponseWriter, session sessionauth.Session) {
    http.SetCookie(w, &http.Cookie{
        Name: "session_id", Value: session.ID, Path: "/",
        HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
        Expires: session.ExpiresAt,
    })
}

func CompleteLogin(w http.ResponseWriter, r *http.Request,
    store *sessionauth.Store, verifiedIdentity gateway.Identity,
) {
    session, err := store.Create(r.Context(), sessionauth.CreateOptions{
        Identity: verifiedIdentity, TTL: sessionTTL,
    })
    if err != nil {
        http.Error(w, "session unavailable", http.StatusServiceUnavailable)
        return
    }
    setSessionCookie(w, session)
    w.WriteHeader(http.StatusNoContent)
}

func sessionCredential(w http.ResponseWriter, r *http.Request) (string, bool) {
    extract, err := gateway.CookieCredential("session_id")
    if err != nil {
        http.Error(w, "invalid server configuration", http.StatusInternalServerError)
        return "", false
    }
    id, err := extract(r)
    if errors.Is(err, gateway.ErrInvalidCredential) || (err == nil && id == "") {
        http.Error(w, "unauthorized", http.StatusUnauthorized)
        return "", false
    }
    if err != nil {
        http.Error(w, "credential unavailable", http.StatusServiceUnavailable)
        return "", false
    }
    return id, true
}

func Refresh(w http.ResponseWriter, r *http.Request, store *sessionauth.Store) {
    id, ok := sessionCredential(w, r)
    if !ok { return }
    session, found, err := store.Renew(r.Context(), id, sessionTTL)
    if err != nil {
        http.Error(w, "session unavailable", http.StatusServiceUnavailable)
        return
    }
    if !found {
        http.Error(w, "unauthorized", http.StatusUnauthorized)
        return
    }
    setSessionCookie(w, session)
    w.WriteHeader(http.StatusNoContent)
}

func Logout(w http.ResponseWriter, r *http.Request, store *sessionauth.Store) {
    id, ok := sessionCredential(w, r)
    if !ok { return }
    if err := store.Revoke(r.Context(), id); err != nil {
        http.Error(w, "session unavailable", http.StatusServiceUnavailable)
        return
    }
    http.SetCookie(w, &http.Cookie{
        Name: "session_id", Value: "", Path: "/", MaxAge: -1,
        HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
    })
    w.WriteHeader(http.StatusNoContent)
}
```

Cookie 的有效期应配合 Redis 会话的 `ExpiresAt`；仅延长浏览器 Cookie 不会续期 Redis 会话。示例面向 HTTPS；本地 HTTP 调试需要业务方明确调整 Cookie 配置。跨站 Cookie 是否采用 `SameSite=None`、是否携带凭证以及对应 CORS 与 CSRF 策略由业务方决定。

TTL 至少 1 毫秒，按毫秒向下取整，不支持永久会话。Redis 服务端时间生成 `CreatedAt` 和 `ExpiresAt`。续期从当前时间重新计算过期时间，可以缩短原有有效期，但不修改身份、创建时间或 Session ID。身份中的权限是创建时的快照，业务权限变化时可通过撤销会话要求重新登录。

## 用户反查与踢下线

管理端完成权限检查后可以调用：

```go
sessions, err := store.ListByUser(ctx, userID)
if err != nil {
    return err
}
// sessions 按创建时间升序排列；同一毫秒按 ID 排序。
// 管理接口应映射为业务 DTO，不能将含秘密 ID 的完整会话列表直接公开。
_ = sessions

// 撤销一个指定会话；业务方需先验证对该会话的操作权限。
if err := store.Revoke(ctx, sessionID); err != nil {
    return err
}

// 撤销此用户在当前项目中的全部有效会话。
return store.RevokeByUser(ctx, userID)
```

默认每用户最多 **10 个有效会话**，`MaxSessionsPerUser=0` 使用该默认值，负数拒绝。满额时创建新会话会原子撤销最早创建的会话；续期不改变淘汰顺序，不会因为最近活跃就避免被淘汰。调低上限后，下次创建会淘汰足够多的旧会话。上限应保持合理，用户查询和原子脚本的工作量随该用户会话数量增长。

会话被撤销后，后续网关认证返回 `401`。已经通过认证并在执行的请求和既有 WebSocket 不会被中断。全部撤销之后的新登录仍允许创建；需要封禁账户时，由业务系统在登录入口控制。

脚本保证续期不能复活已撤销的会话。与全部撤销并发的新建操作，按 Redis 实际执行顺序决定是否被撤销。单会话撤销和按用户全部撤销均可重复执行。

## 键空间、索引与存储约束

```go
keys, err := sessionauth.NewKeyBuilder(sessionauth.KeyConfig{})
if err != nil { return err }
key, err := keys.SessionKey("kgraph", sessionID)
// kgraph:session:<sessionID>
if err != nil { return err }
index, err := keys.UserSessionsKey("kgraph", userID)
// kgraph:user_sessions:<userID>
if err != nil { return err }
_, _ = key, index
```

函数只接收标识部分，固定段由 KeyBuilder 补齐；尖括号是文档占位符，实际不写入 Redis。分隔符默认 `:`，可以通过 `KeyConfig.Separator` 指定 ASCII 标点组合，但不能包含 `%` 或大括号。标识不能为空，也不会自动裁剪。百分号、分隔符中的每个字节、大括号、空白／控制字符及非 ASCII 字节统一转义为大写 `%HH`，防止输入与转义结果发生碰撞。

Session ID 由 32 字节密码学随机数编码为无填充 base64url。会话记录带内部版本及身份完整性校验值；校验值只用于发现意外损坏，不是签名，Redis 写权限属于认证可信边界。存储编码是包的内部实现，不是其他语言直接写入 Redis 的公共协议。业务方必须通过 Store 操作会话和索引。

用户索引用有序集合按创建时间排序，读取时过滤并清理已过期成员；索引 TTL 覆盖最后一个有效会话。没有全库 `KEYS` 或扫描反查，也不依赖 Redis 过期事件通知。创建、续期、撤销和查询在单个 Redis 主节点原子执行，所有可预见的数据校验先于写入；脚本运行时或网络故障不提供事务回滚保证。

身份 `Attributes` 必须可以 JSON 编码。存储时保留身份原始 JSON，避免 Lua 重编码损失空数组或大整数；读取到 Go 的 `map[string]any` 后，数字遵循 `encoding/json` 默认规则成为 `float64`。需要精确往返的业务大整数应使用字符串，不能依赖自定义 Go 类型在存储后保留。

不存在或过期是正常认证拒绝；Redis 连接／命令故障、损坏数据和未知存储版本返回错误，网关按 `503` 拒绝。调用方可通过 `errors.Is(err, sessionauth.ErrCorruptSession)` 识别数据损坏，通过 `ErrSessionCollision` 识别多次 ID 冲突；底层错误保留在错误链中，不应直接作为 HTTP 正文输出。

凭证错误码与装配迁移见[网关凭证提取迁移](gateway.md#可插拔凭证提取迁移)。

## 验证

```sh
make integration-session
make go-module-consumer
make verify
```

`make integration-session` 启动固定为 Redis `8.2.7-alpine` 及不可变 digest 的临时容器，只向本机随机端口暴露，不启用持久化；运行真实 Lua、TTL、并发和 Cookie 认证测试，包含 race 检查，退出时清理容器及匿名卷。镜像来自 Docker Hub 官方 Redis 仓库；运行需要 Docker 和镜像仓库访问能力，不需要安装生产 Redis。

普通 `go test` 未设置 `STELLARMESH_SESSION_REDIS_ADDR` 时跳过真实 Redis 用例。CI 的 Go 检查任务明确执行 `make integration-session`，不会用跳过的测试代替 Redis 验证。`make verify` 保持全仓静态、单元、浏览器与消费检查；`make integration` 同时覆盖 Storage 和 Session 的容器集成。
