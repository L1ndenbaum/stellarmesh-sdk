# Gateway 迁移

[返回接入入口](gateway.md)。场景代码为装配片段；[完整可执行示例](../../../sdk/go/gateway/example_test.go)演示本地代理与资源关闭。

## 从 `v0.1.0` 升级到 `v0.2.0`

`v0.1.0` 默认返回带 `code`、`message`、`data`、`timestamp` 和 `error_reason` 的 Stellarmesh JSON envelope；`v0.2.0` 改为协议中立的纯文本。升级前应检查调用方、探针和前端是否解析默认错误正文或健康响应。需要保留原结构时，先在项目仓库实现上面的两个响应器并完成契约测试，再升级 Module。已经显式配置 `WithErrorResponder` 的项目继续保留自己的错误正文，并会在响应器执行前获得 SDK 设置的 `Retry-After`。

## `v0.3.1` 访问日志修正

待发布的 `v0.3.1` 保留各已执行限流阶段的 `allowed`、`rejected`、`error`、`disabled` 结果，默认 slog 输出会包含相应 `rate_limit_result`。请求提前结束时不补造尚未执行阶段的结果。自定义 `AccessLogger` 获得独立的结果 map 和 Roles slice，修改日志副本不会改写请求原始状态；日志错误或 panic 仍属于旁路故障，不改变响应。

## 从 `v0.2.0` 升级到 `v0.3.0`

`v0.3.0` 删除 Gateway Core 中的 `WithAccessLogEmitter` 和对 Stellarmesh Logging 的直接依赖，改为默认使用标准库 `slog`。升级项目需要：

1. 检查默认 CLI 访问日志的容量与日志等级，或显式调用 `WithoutAccessLog()`；
2. 删除远程Emitter、Client和关闭生命周期，使用默认AccessLogger或注入项目自己的`WithAccessLogger`；
3. 在composition root配置标准库JSON Handler和日志等级，再交给项目Collector采集；
4. 验证访问日志失败仍不会改变业务响应，身份字段仍默认关闭。

本次 SDK 不包含共享 gateway 可执行程序，也不包含服务发现、动态配置、配置热更新、自动重试、熔断、WAF、缓存、灰度路由或管理控制面。这些能力应在出现明确的跨项目需求后独立设计。

## 可插拔凭证提取迁移

`WithAuthenticator(authenticator)` 继续默认提取 Bearer 凭证，也可以传入一个 `CredentialExtractor` 函数。使用 Cookie 时先调用 `CookieCredential(name)` 校验名称，再将返回值作为第二个参数。提取器不读取认证存储、不写响应；返回空字符串表示缺失，返回或包装 `ErrInvalidCredential` 表示格式错误，其他错误视为组件故障。内置提取器拒绝重复认证头、重复同名 Cookie 和格式错误，不自动回退到其他凭证来源。公开路由跳过提取和认证。

认证错误码统一为 `missing_credential`、`invalid_credential`，替代 `missing_bearer_token`、`invalid_bearer_token`；访问日志对应使用 `missing_credential`、`invalid_credential`。缺失与无效凭证返回 `401`，提取器故障返回 `503 credential_extraction_failed`，认证器故障仍返回 `503 authentication_failed`。错误与日志不包含凭证内容。

`WithAuthenticator` 改为变参函数后，原有普通调用保持有效；若将函数本身赋给固定签名的函数变量，需要同步更新变量签名或加一层闭包。显式传入空提取器或多个提取器会在构造时被拒绝。

## Redis Session 初版

主干新增 `gateway/sessionauth`（尚未发布），与 `jwtauth` 并列。`NewStore(StoreConfig)` 接收 Redis Client、项目 `ProjectScope`、可选 `KeySeparator` 和 `MaxSessionsPerUser`；提供 `Create`、`Lookup`、`Renew`、`Revoke`、`ListByUser`、`RevokeByUser`。`NewAuthenticator(store)` 接入 `WithAuthenticator`，Cookie 来源通过 `CookieCredential(name)` 显式装配。

会话默认按项目使用 `project:session:sessionID` 和 `project:user_sessions:userID`；`KeyBuilder.SessionKey`、`KeyBuilder.UserSessionsKey` 统一拼接和转义。默认每用户最多 10 个有效会话，创建第 11 个时淘汰最早创建的会话；续期不改变创建顺序。TTL 必须至少 1 毫秒，精度为毫秒；Redis 服务端时间决定过期，认证不自动续期。按用户全部撤销后允许重新登录，也不强制中断已在执行的请求或长连接。

该包依赖 Redis 和现有 `go-redis/v9`，只支持单实例或 Sentinel，不支持 Redis Cluster。Redis 安装、部署、Client 创建和关闭由业务方负责；SDK 不接管登录校验、Cookie 设置、CSRF 或管理端权限。会话与索引只能通过 Store 修改；同一项目应统一 scope、分隔符和会话上限。Session ID 是秘密凭证，不应输出到日志或未经授权的列表接口。身份 Attributes 必须可 JSON 编码，Go 读取后的数字为 `float64`，不保留自定义 Go 类型。

完整的 Cookie 登录、续期、退出、用户反查与踢下线示例见 [Redis Session 接入教程](sessionauth.md)。

