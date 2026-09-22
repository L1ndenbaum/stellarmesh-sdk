# 前端迁移说明

[返回接入入口](README.md)。本文场景片段配合[完整示例](../../../sdk/frontend/examples/quickstart.ts)阅读；业务 DTO 与回调由项目提供。

## 全声明式入口迁移

旧的 `httpClient`、`createHttpApi(client)`、`HttpClient`、`ConfigurableHttpClient` 和 `HttpBodyMethod` 不再导出，不提供兼容别名。直接导入 `http` 并链式派生配置；六种方法统一返回请求函数，不能再将方法声明当成 Promise 使用。

旧 `get<TResponse>(url, options)` 改为声明 `get<void, TResponse>(url)` 后调用 `request(undefined, options)`；有查询参数时改为 `get<TQuery, TResponse>(url)` 后传入查询。旧 `post<TBody, TResponse>(url, body, options)` 改为声明后调用 `request(body, options)`。旧 `requestWithMetadata` 改为 `withMetadata()` 加普通或通用请求声明；旧请求对象改为同步映射结果，`signal` 移到返回函数的第二个参数。

## 未发布初版的认证接口迁移

本次为破坏性调整，不提供旧契约兼容分支：

- `getAccessToken()` 改为 `getAuthHeaders({ epoch })`；Bearer 项目显式返回 `{ Authorization: 'Bearer ' + token }`，无凭证时返回 `{}`。Cookie Session 可省略该回调。
- `refreshSession()` 不再返回 Token 或 `null`。项目先完成凭证条件保存，再返回 `AuthRefreshResult.REFRESHED`；确认无法恢复时返回 `AuthRefreshResult.EXPIRED`，其他失败抛出异常。
- 配置刷新时必须同时提供 `shouldRefresh`。不再默认匹配 HTTP 401；`onUnauthorized` 只能与完整刷新配置一起出现。不完整配置在类型检查和运行时均被拒绝。
- 浏览器跨源 Cookie 通过第二个绑定参数 `{ withCredentials: true }` 开启，遵循绑定的 `trustedOrigins`；登录和刷新接口使用 `authRecovery: false` 保留 Cookie 行为。
- Axios 的默认 XSRF 推断已关闭，原先依赖该行为的项目必须显式提供 CSRF 头。

更早的 `withAuth(options)` 仍需迁移为 `withAuth(createAuthSession(options), bindingOptions)`。继续提供必需的 `getSessionEpoch`，将 `trustedOrigins` 放在绑定参数；跨客户端共享时复用同一个 `AuthSession`。项目在实际保存或清理处执行 epoch 条件提交，临时刷新失败不能转换为确认失效。

## 从 XieHe 接入

- 从 SDK 导入 `http` 并在公共模块派生配置；将业务接口改为 `post<TBody, TResponse>(url, defaults)` 声明，再通过返回函数传入 body 和单次配置。
- 使用严格信封处理前确认接口响应；确有混合响应时显式选择透传或单次 `responseMode: 'raw'`。
- 鉴权开关改为布尔值；旧 `auth: 'none'` 改为 `auth: false`。
- 保留项目公共 API、鉴权 API 和对象存储的独立装配，注入已有会话桥接。
- 尾斜杠 404 重试、分页兼容、业务 DTO 和上传流程继续留在项目内，先修正路由或在项目适配层过渡。
- 本次仅验证对应 SDK 行为，没有修改 XieHe 或完成其运行时迁移。

