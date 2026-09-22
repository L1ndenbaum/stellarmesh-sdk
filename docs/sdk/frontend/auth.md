# 会话与认证恢复

[返回接入入口](README.md)。本文场景片段配合[完整示例](../../../sdk/frontend/examples/quickstart.ts)阅读；业务 DTO 与回调由项目提供。

## 鉴权和并发刷新

`AuthSession` 只协调会话生命周期和恢复，不解析 JWT，也不固定使用 Bearer。业务方负责提供认证请求头、判断响应错误、调用刷新接口和保存凭证。

```ts
import { createAuthSession, http } from '@stellarmesh/sdk';

const auth = createAuthSession({
  getSessionEpoch: () => session.getEpoch(),
  getAuthHeaders: ({ epoch }) => session.getAuthHeaders(epoch),
  shouldRefresh: error =>
    error.status === 401 && error.apiCode === 'ACCESS_TOKEN_EXPIRED',
  refreshSession: ({ epoch }) => session.refreshAndSave(epoch),
  onUnauthorized: (error, { epoch }) => session.handleExpired(error, epoch),
});

const apiClient = http.withBaseURL(apiBaseURL).withAuth(auth);
const slowApiClient = apiClient.withTimeout(60_000);
const otherApiClient = http.withBaseURL(apiBaseURL).withAuth(auth);
```

`apiBaseURL` 和 `session` 由项目实现或注入。三个客户端显式共享同一认证会话，配置仍各自独立；两次 `createAuthSession(options)` 会创建独立的刷新协调状态。`withAuth` 只接受工厂返回的会话。

`shouldRefresh` 与 `refreshSession` 必须同时提供或同时省略；只有启用刷新后才能配置 `onUnauthorized`。SDK 不提供默认谓词，不会因为 HTTP 401 自动刷新。上述错误码仅为业务示例，也可以从 `error.data` 读取其他后端错误结构。HTTP 错误体的 JSON 解析不依赖 `flattenEnvelopeResponse()`。

这些配置遵循 fail-closed：TypeScript 拒绝不完整组合，JavaScript 调用在创建时也会得到 `TypeError`；非函数回调、非法 `withCredentials` 同样被拒绝。谓词必须同步返回布尔值，刷新必须返回 `AuthRefreshResult.REFRESHED` 或 `AuthRefreshResult.EXPIRED`，不采用真值转换。

`getAuthHeaders({ epoch })` 可同步或异步返回普通请求头对象，名称必须有效、值必须是字符串；省略或返回 `{}` 表示无需注入认证头。SDK 复制返回值，并按大小写不敏感的名称覆盖客户端、声明、映射及调用配置中的同名头。Bearer、其他认证方式和 CSRF 请求头均由业务方构造。刷新成功后重新读取整份认证头，不残留上一次注入的字段。

| 刷新结果或请求条件 | SDK 行为 |
| --- | --- |
| 返回 `AuthRefreshResult.REFRESHED` | 复核 epoch，重新读取认证头，重放原请求一次；Cookie 使用浏览器更新后的状态 |
| 返回 `AuthRefreshResult.EXPIRED` | 项目确认无法恢复，调用可选的 `onUnauthorized`，再抛出响应错误 |
| 刷新或认证头回调抛出异常 | 传播错误，不通知退出，不作为业务传输失败重试 |
| 返回旧 Token 字符串、`null`、布尔值或其他非法刷新结果 | 抛出 `kind: 'auth'` 的契约错误，保留 `TypeError` cause，不重放或通知退出 |
| 重放后再次出现匹配的认证失败 | 不再次刷新，通知退出并抛出响应错误 |
| 谓词返回 `false`、未启用刷新或 `authRecovery: false` | 不恢复也不通知退出，交给调用方处理 |

`shouldRefresh` 只接收 HTTP 或业务错误；网络、超时、取消、会话变化、响应格式错误和认证回调自身异常不参与恢复判断。HTTP 200 内的业务认证错误需要响应转换先抛出 `HttpClientError(kind: 'business')`，例如使用信封转换并显式匹配 `apiCode`。已有 `HttpClientError` 原样保留，Axios 错误保留归一化后的类别与 cause；其他回调异常包装为 `kind: 'auth'`。临时网络故障、服务不可用或凭证保存失败不能视为确认失效。

同一 epoch 内，并发及迟到的旧请求共享所属刷新代次的 Promise 和结果，包括失败。刷新完成后的新请求可进入新代次；取消一个等待者不会取消其他请求共享的刷新。退出通知在对应代次内去重。

`getSessionEpoch()` 为同步函数，返回字符串或数字。登录、退出、切换账号或替换会话时必须更换且不可复用 epoch；同一会话正常刷新不更换。SDK 在读取认证头、发送、刷新、重放和交付成功结果的异步边界检查 epoch。会话变化后抛出 `kind: 'session-changed'`，不跨账号重放或通知旧会话退出。

**项目仍需在实际保存、清理凭证时执行 epoch 条件检查。** 异步存储应使用条件写入或锁；SDK 的事后检查不能撤销已写入的凭证，也不能撤销浏览器已经处理的 `Set-Cookie`。Cookie Session 的失效与账号切换需要后端配合。SDK 不会撤销已发出的服务端操作，会话对象也不应作为 SSR 进程级单例。

可信来源和 Cookie 携带属于绑定配置：`.withAuth(auth, { trustedOrigins, withCredentials })`。省略 `trustedOrigins` 时信任 baseURL 的 origin，无 baseURL 时在浏览器使用当前 origin；显式空数组不信任任何目标。派生保留原绑定，不合并不同客户端的可信范围。仅对可信目标读取认证头、参与会话恢复，并在 `withCredentials: true` 时主动开启浏览器跨源 Cookie 携带。

`authRecovery: false` 保留认证头和 Cookie 行为，只关闭自动恢复及退出通知；登录和刷新接口应使用该配置或没有恢复能力的独立客户端，避免递归刷新。`auth: false` 关闭 SDK 认证回调、自动注入、主动跨源 Cookie 携带及恢复，但不清除调用方手动配置的请求头，也不禁止浏览器默认同源 Cookie。

SDK 关闭 Axios 默认的 `XSRF-TOKEN` → `X-XSRF-TOKEN` 推断。需要 CSRF 保护时，由业务方在 `getAuthHeaders` 中提供后端约定的头；SDK 不读取特定 Cookie 或内置 CSRF 获取接口。Cookie、CORS、重定向和服务端鉴权策略仍由浏览器及业务后端执行。

## Bearer 会话的条件提交示例

下面演示同步内存状态的完整提交边界。`refreshAndValidate` 由项目实现，使用对应 epoch 的刷新凭证调用独立刷新客户端，只把明确的凭证失效转换为 `null`，其余异常抛出。

```ts
import {
  createAuthSession,
  AuthRefreshResult,
  HttpClientError,
  type AuthSessionEpoch,
  type HttpHeaders,
} from '@stellarmesh/sdk';

function createProjectSession(
  refreshAndValidate: (epoch: AuthSessionEpoch) => Promise<string | null>,
) {
  let state: { epoch: number; accessToken: string | null } = {
    epoch: 0,
    accessToken: null,
  };

  function assertEpoch(expected: AuthSessionEpoch) {
    if (state.epoch !== expected) {
      throw new HttpClientError('会话已变化', { kind: 'session-changed' });
    }
  }

  function replaceSession(accessToken: string | null) {
    state = { epoch: state.epoch + 1, accessToken };
  }

  const auth = createAuthSession({
    getSessionEpoch: () => state.epoch,
    getAuthHeaders: ({ epoch }): HttpHeaders => {
      assertEpoch(epoch);
      return state.accessToken
        ? { Authorization: `Bearer ${state.accessToken}` }
        : {};
    },
    shouldRefresh: error =>
      error.status === 401 && error.apiCode === 'ACCESS_TOKEN_EXPIRED',
    refreshSession: async ({ epoch }) => {
      const accessToken = await refreshAndValidate(epoch);
      assertEpoch(epoch);
      if (accessToken === null) return AuthRefreshResult.EXPIRED;
      if (!accessToken.trim()) throw new Error('刷新返回了空 Token');
      // 检查与写入之间没有 await；正常刷新保留原 epoch。
      state = { ...state, accessToken };
      return AuthRefreshResult.REFRESHED;
    },
    onUnauthorized: (_error, { epoch }) => {
      assertEpoch(epoch);
      // 退出也更换 epoch，使其他旧请求失效。
      replaceSession(null);
    },
  });

  return { auth, replaceSession };
}
```

项目在登录成功、用户主动退出和切换账号时调用 `replaceSession`；SSR 每个用户请求单独创建这个适配器。示例中的 epoch 只在该适配器生命周期内递增，不能将同一个认证会话对象绑定到重新从零计数的状态。

若真实凭证保存在异步存储中，不能把示例中的赋值简单替换为 `await save(...)`。必须在存储实际提交点对 epoch 条件写入，或让保存、退出和账号切换共同使用同一把锁；检查后再等待写入，仍可能覆盖后来登录的账号。异步清理同样必须在实际删除前核对 epoch。UI 跳转等延迟副作用也应受对应 epoch 约束。

SDK 只能在异步阶段结束或下一次发送前检测变化，不会订阅项目登录事件、撤销已发出的写操作，或取消共享刷新中的项目保存任务。项目需要立即停止页面请求时，仍应自行触发相应 `AbortController`。

## 浏览器 Cookie Session 示例

Cookie 由浏览器管理，业务方不需要读取 HttpOnly Cookie，也不需要在刷新后返回 Token。下面约定后端 `/session/refresh` 成功时通过 `Set-Cookie` 更新会话，返回 HTTP 204；只有明确的 `SESSION_NOT_REFRESHABLE` 表示无法恢复。示例使用顶层 `code`，项目可替换为自己的 `error.data` 判断。

```ts
import {
  http,
  createAuthSession,
  AuthRefreshResult,
  HttpClientError,
  isHttpClientError,
} from '@stellarmesh/sdk';

function createCookieApi(baseURL: string) {
  let epoch = 0;
  const auth = createAuthSession({
    getSessionEpoch: () => epoch,
    shouldRefresh: error =>
      error.status === 401 && error.apiCode === 'SESSION_EXPIRED',
    refreshSession: async () => {
      try {
        await requestRefresh();
        return AuthRefreshResult.REFRESHED;
      } catch (error) {
        if (
          isHttpClientError(error) &&
          error.status === 401 &&
          error.apiCode === 'SESSION_NOT_REFRESHABLE'
        ) return AuthRefreshResult.EXPIRED;
        throw error;
      }
    },
    onUnauthorized: (_error, context) => {
      if (epoch !== context.epoch) {
        throw new HttpClientError('会话已变化', { kind: 'session-changed' });
      }
      epoch++;
      // 项目在此更新登录状态；HttpOnly Cookie 由后端清理或失效。
    },
  });
  const api = http.withBaseURL(baseURL).withAuth(auth, {
    withCredentials: true,
  });
  // 关闭恢复仍保留 Cookie，刷新失败直接交给上面的业务回调判断。
  const requestRefresh = api.post<void, void>('/session/refresh', {
    authRecovery: false,
  });
  const requestLogin = api.post<{ username: string; password: string }, void>(
    '/session/login',
    { authRecovery: false },
  );
  const requestWorkspace = api.get<void, { id: string }>('/workspace');

  return {
    requestLogin,
    requestWorkspace,
    replaceSession: () => { epoch++; },
  };
}
```

项目在登录完成、退出或切换账号时调用 `replaceSession()`。刷新结果返回后 SDK 会再次检查 epoch，但浏览器可能已处理该响应的 `Set-Cookie`；后端仍须处理旧刷新与退出、账号切换的竞争。需要 CSRF 头时为该会话补充 `getAuthHeaders`，登录及刷新接口会正常调用它。

同源 Cookie 使用浏览器默认行为，可以省略 `withCredentials`；跨源时必须显式开启，且目标必须可信。服务端需正确配置 CORS（包括具体来源和允许凭证），Cookie 还受 `SameSite`、`Secure` 和浏览器第三方 Cookie 策略限制。`withCredentials: false` 不会禁止同源 Cookie；不能用 `auth: false` 断言请求完全匿名。

本轮仅提供浏览器 Cookie 管理能力。在 Node 执行需要主动 Cookie 携带的请求会抛出 `kind: 'auth'`，SDK 不会创建 Cookie 容器或自动转发 SSR 用户 Cookie；普通请求头认证和 Node ESM 包消费继续支持。


认证装配的可执行最小示例见[认证与流示例](sse.md#可执行的认证与流示例)，其刷新和受保护请求通过同一份安装制品验证。
