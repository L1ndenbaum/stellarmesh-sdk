# 前端 HTTP SDK 接入

前端包位于 `sdk/frontend/`，包名暂定为 `stellarmesh-sdk`，源码版本 `0.1.0`，尚未发布 npm。它封装 Axios 传输、响应处理、鉴权刷新、重试和对象字节传输，不包含项目 DTO、分页兼容、上传会话或页面状态。

维护 SDK 时，目录职责和内部依赖约定见[代码组织说明](../../../sdk/frontend/README.md#代码组织)；业务项目继续从包根入口导入。

## 声明式接口

业务 API 先声明，再由 Application 调用。统一从 `http` 根对象派生配置，声明和配置过程都不发送网络请求：

```ts
// shared/api/http.ts
import {
  http as rootHttp,
  flattenEnvelopeResponse,
} from 'stellarmesh-sdk';

export const http = rootHttp
  .withBaseURL('/api')
  .withTimeout(15_000)
  .withResponseTransform(flattenEnvelopeResponse());
```

需要认证时，在装配客户端阶段通过 `.withAuth(auth)` 注入项目会话。业务模块使用项目自己的 DTO 声明接口，SDK 不依赖生成代码：

```ts
import { http } from '@/shared/api/http';
import type {
  LoginRequest,
  Token,
  FaultTracingWorkspaceResponse,
} from '@/shared/api/generated/backend';

export const requestLogin =
  http.post<LoginRequest, Token>('/auth/login', { auth: false });

export const requestWorkspace =
  http.get<void, FaultTracingWorkspaceResponse>('/fault-tracing/workspace');
```

调用时传入本次数据：

```ts
const session = await api.requestLogin({ username, password });
const workspace = await api.requestWorkspace();
```

| 声明方法 | 泛型顺序 | 返回函数的第一个参数 |
| --- | --- | --- |
| `get`、`head`、`delete` | `<TQuery, TResponse>` | 查询对象或 `URLSearchParams`，映射到 `params`，不发送请求体 |
| `post`、`put`、`patch` | `<TBody, TResponse>` | 请求体，映射到 `data` |
| 输入为 `void` 的任一方法 | `<void, TResponse>` | 可以省略，直接 `requestXxx()` |

声明方法统一采用输入泛型在前、响应泛型在后。普通查询 DTO 接口无需额外声明字符串索引签名：

```ts
interface ListUsersQuery {
  page: number;
  keyword?: string;
}

const requestUsers =
  http.get<ListUsersQuery, { items: string[] }>('/users');

const users = await requestUsers({ page: 1, keyword: '张' });
```

### 声明默认配置与调用配置

声明方法的第二个参数是默认配置；返回函数的第二个参数始终是单次调用配置，不根据对象内容猜测数据或配置：

```ts
const requestLogin = http.post<LoginRequest, Token>('/auth/login', {
  auth: false,
  timeout: 10_000,
});

const session = await requestLogin(
  { username, password },
  { signal: controller.signal, timeout: 5_000 },
);

const workspace = await requestWorkspace(undefined, {
  signal: controller.signal,
});
```

- 优先级为调用配置、声明配置、客户端配置、SDK 默认值。调用配置中的 `undefined` 表示沿用默认值，`false`、`0` 等显式值正常覆盖。
- headers 按大小写不敏感名称合并；同名值由更具体的配置覆盖。SDK 的自动 Bearer 注入规则继续生效。
- `signal` 仅允许在调用阶段提供，不能绑定到可复用声明。一次取消不会影响同一声明的其他并发调用或后续调用。
- GET／HEAD／DELETE 的查询参数只从输入提供，声明配置和调用配置均不接受 `params`。
- POST／PUT／PATCH 可在配置中提供额外 `params`。调用值替换整个声明查询容器，不逐字段合并；传入空对象或空 `URLSearchParams` 可清空声明默认查询，URL 字符串本身的查询部分仍保留。
- 声明保存配置快照，并复制 headers、params 的外层容器以及 `URLSearchParams` 内容；每次调用分别组装配置。嵌套查询值、请求体和回调不会深拷贝，调用方应在使用期间保持它们稳定。

### 执行与生命周期

声明只绑定客户端、方法、固定 URL 字符串及默认配置，不读取认证请求头、不捕获会话 epoch、不发送网络请求。每次调用才进入原客户端执行流程，读取当前会话，独立计算重试与认证恢复额度；不会缓存 Promise 或自动去重，错误也不会额外包装。信封转换、可信 origin、`auth: false` 和 `authRecovery: false` 均沿用下文规则。

`HttpApi` 是唯一公开声明接口；配置仍不可变，后续派生不会改变已声明函数绑定的配置。SSR 必须按用户请求装配会话及声明入口，不能跨用户共享绑定了会话的声明函数。需要替换业务 API 的测试可注入对应的请求函数，SDK 不再导出立即发送客户端。

## metadata 返回

`withMetadata()` 只选择 HTTP 信息返回形态，不负责解开业务信封。派生后继续配置其他选项会保留 metadata 模式；重复调用不会嵌套包装，也不改变原入口。

```ts
const requestWorkspaceInfo = http
  .withMetadata()
  .get<void, FaultTracingWorkspaceResponse>('/fault-tracing/workspace');

const response = await requestWorkspaceInfo();
const workspace = response.data;
const status = response.status;
const etag = response.headers.etag;
```

`TResponse` 始终表示转换后的数据。普通模式返回 `Promise<TResponse>`，metadata 模式返回 `Promise<HttpResponse<TResponse>>`，其中 headers 名称统一小写。两种模式使用相同执行和响应转换流程，错误照常抛出。

假设服务端返回 `ApiEnvelope<DTO>`：启用 `flattenEnvelopeResponse()` 时，普通模式取得 DTO，metadata 模式取得 `HttpResponse<DTO>`；未启用响应转换时，两者分别取得信封与 `HttpResponse<ApiEnvelope<DTO>>`。SDK 不因未配置转换而自动添加信封。

## 动态路径和通用请求声明

`request<TInput, TResponse>(resolve, defaults?)` 接收同步映射函数并返回可复用请求函数。映射结果为 `HttpApiRequestDescriptor`，必需 `method`、`url`，可选 `params`、`data`、`headers`。不采用额外的路径绑定或柯里化阶段：

```ts
import { HttpMethod } from 'stellarmesh-sdk';

type UpdateUserInput = {
  userId: string;
  body: UpdateUserRequest;
};

const requestUpdateUser = http.request<UpdateUserInput, User>(
  ({ userId, body }) => ({
    method: HttpMethod.PATCH,
    url: `/users/${encodeURIComponent(userId)}`,
    data: body,
  }),
);

const user = await requestUpdateUser(
  { userId, body: { displayName: '张三' } },
  { signal: controller.signal },
);
```

- 映射只在每次逻辑调用时执行一次；声明时不执行，认证恢复与普通重试复用已经组装的请求。已取消调用不运行映射；映射异常或缺少有效方法与 URL 的描述产生拒绝的 Promise 且不发送网络请求，普通异常按 `kind: 'unknown'` 保留 cause。
- 方法、URL、查询与请求体仅来自映射，不自动转发整个输入；通用声明和调用配置均不接受 `params`。DELETE 请求体通过映射的 `data` 表达，GET／HEAD 便捷方法仍不发送请求体。
- 普通配置保持调用、声明、入口配置、SDK 默认值的优先级。通用请求的 headers 依次按调用配置、映射结果、声明配置、入口配置覆盖，名称大小写不敏感。
- 路径片段由项目显式编码，完整 URL 原样交给传输层。签名获取、续签和路径模板不由 SDK 自动处理；动态目标同样受可信 origin 判断约束。
- `HttpMethod` 和 `ResponseType` 同时提供运行时常量与同名类型，例如 `HttpMethod.GET`、`ResponseType.JSON`；字符串字面量及 `import type` 仍可使用。

## 配置派生

`withBaseURL`、`withHeaders`、`withTimeout`、`withMaxRetries`、`withRetry`、`withAuth`、`withResponseTransform` 和 `withMetadata` 均返回新声明入口，必须接住返回值。认证会话引用在普通派生中共享，显式更换会话时才改变；不同入口不会合并可信 origin。

`withResponseTransform` 替换当前转换器，支持同步或异步结果。取消只停止当前调用的等待，不强行终止转换器自身任务。接口不读取业务环境变量或浏览器存储，不依赖 React、路由器或项目上下文，不暴露 Axios 实例。

## 信封和响应类型

普通请求返回 `Promise<TResponse>`，其中 `TResponse` 指最终数据。未启用转换时返回 HTTP 响应体，不返回 AxiosResponse；启用信封处理后返回信封内的 `data`。

```ts
const client = http.withResponseTransform(
  flattenEnvelopeResponse({
    isSuccess: code => code === 'OK',
    allowNonEnvelope: false,
  }),
);
```

默认要求 `code` 为数字或字符串、`message` 为字符串；成功码默认是数字 `0` 或 `200–299`。这是工具的可替换默认策略，不是要求所有语言和项目采用的公共协议。

- 成功信封返回 `data`；`null` 原样保留，缺失 `data` 返回 `undefined`。允许空值的调用应声明 `T | null`，无内容调用应声明 `void`。
- 失败信封抛出 `kind: 'business'`，HTTP 200 不会覆盖业务失败。
- 非信封默认抛出 `kind: 'response-format'`；迁移需要混合响应时可显式启用 `allowNonEnvelope`。
- HTTP 204 和 HEAD 返回 `undefined`，不运行转换器。
- 单次 `responseMode: 'raw'` 跳过转换，返回完整响应体；`text`、`blob`、`arraybuffer` 同样跳过 JSON 响应转换。无响应类型配置时按 JSON 解析，非法 JSON 抛出格式错误。
- TypeScript 泛型不会验证 DTO 内容；信封检查只验证外壳，不证明业务字段存在或类型正确。

需要 HTTP 信息时，在声明前派生 `withMetadata()`；响应转换仅作用于 `data`，不会重复执行。

## 鉴权和并发刷新

`AuthSession` 只协调会话生命周期和恢复，不解析 JWT，也不固定使用 Bearer。业务方负责提供认证请求头、判断响应错误、调用刷新接口和保存凭证。

```ts
import { createAuthSession, http } from 'stellarmesh-sdk';

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
} from 'stellarmesh-sdk';

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
} from 'stellarmesh-sdk';

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

## 超时、重试和取消

默认 `timeout: 0`、`maxRetries: 0`。`withTimeout(ms)` 是每次 Axios 传输尝试的超时，不是独立 TCP/TLS 建连超时，也不包含刷新和退避等待。调用方可通过 `signal` 限制整个操作的生命周期。

```ts
const client = http.withRetry({
  maxRetries: 2,
  baseDelayMs: 300,
  maxDelayMs: 10_000,
});

const requestItems = client.get<void, Item[]>('/items');
await requestItems(undefined, { signal: controller.signal });
```

开启后，默认仅 GET/HEAD 对网络错误、超时、HTTP 408、429、502、503、504 重试。`maxRetries: 2` 表示首次尝试外最多重试两次。使用指数退避与随机抖动，默认基础 300 ms、等待上限 10 s。合法 `Retry-After` 是最小等待时长；超过上限直接返回原错误，非法值忽略。

POST/PATCH/PUT/DELETE 默认不自动重试。调用方确认服务端幂等语义、签名有效和请求体可重放后，在单次请求设置 `retryable: true`。`retryable: false` 可关闭 GET 重试；`maxRetries` 可覆盖实例额度。

每个逻辑请求最多进行一次认证恢复重放；普通传输重试另行计数，整个逻辑请求共享原额度，认证恢复不重置该额度。例如配置一次普通重试后，允许业务发送经历 `401 → 认证恢复重放 503 → 普通重试 200`，共发送三次。SDK 使用原执行循环重放，不重新调用公开请求方法。

`retryable: false` 只关闭普通重试；`authRecovery: false` 只关闭认证恢复及退出通知，仍正常附带凭证。不可重放的请求体或特殊签名请求应关闭认证恢复。允许恢复的写接口必须保证认证失败发生在业务副作用之前；关键写操作应配合服务端幂等机制。需要业务请求最多两次发送时，将普通重试设为 `0`。

取消、会话变化、格式错误和通常的其他 4xx 不触发普通重试；信封业务错误只有显式命中认证谓词才恢复。谓词不匹配或关闭认证恢复不影响适用的普通传输重试。超时不能证明写入失败，SDK 不生成幂等键或代替服务端去重。

## 对象存储传输

对象存储从无鉴权的根对象创建独立实例，不继承业务实例的 headers 或信封处理。项目先获得预签名请求，再原样提供 URL、方法和必要 headers；对象字节不经过业务 API 客户端。

```ts
const storage = http.withTimeout(60_000);
type UploadInput = {
  url: string;
  file: Blob;
  headers: Record<string, string>;
};

const requestUpload = storage.withMetadata().request<UploadInput, string>(
  ({ url, file, headers }) => ({ method: 'PUT', url, data: file, headers }),
  { responseType: 'text', auth: false, authRecovery: false },
);
const requestDownload = storage.request<string, Blob>(
  url => ({ method: 'GET', url }),
  { responseType: 'blob' },
);

const response = await requestUpload(
  { url: signedUrl, file: chunk, headers: signedHeaders },
  {
    signal: controller.signal,
    onUploadProgress: ({ loaded, total }) => updateProgress(loaded, total),
  },
);
const etag = response.headers.etag;
const file = await requestDownload(downloadUrl, {
  signal: controller.signal,
});
```

进度回调提供 `loaded` 和可选 `total`，不保证事件次数或固定频率。Blob、ArrayBuffer、FormData、文本均可作为适用的请求体；使用 FormData 时由运行时生成 multipart boundary，不手工写入不完整的 Content-Type。

浏览器读取跨域 `ETag` 需要对象存储 CORS 暴露该响应头。SDK 保留 ETag 原值，不剥除引号，不推测上传成功后的业务状态。预签名过期、上传会话、分片调度、合并确认和失败补偿属于项目层；重试不会重新签名 URL。

## 错误处理

```ts
import { isHttpClientError } from 'stellarmesh-sdk';

try {
  const requestItems = apiClient.get<void, Item[]>('/items');
  await requestItems();
} catch (error) {
  if (isHttpClientError(error) && error.kind === 'canceled') return;
  throw error;
}
```

`HttpClientError` 提供 `kind`、`status`、`apiCode`、`data`、`headers` 和 `cause`。类别包括 `http`、`business`、`network`、`timeout`、`canceled`、`response-format`、`auth`、`session-changed`、`unknown`。原始响应和 cause 可能含有业务数据或 Axios 请求配置，不能未经清洗直接记录；SDK 不自动记录 URL、请求体或凭据。

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

## 构建与验证

使用 Node 24 和 npm。包提供 ESM 与 `.d.ts`，只导出根入口，不依赖消费者编译仓库源码。初版验证浏览器 HTTP 场景和 Node ESM 制品消费，未验证 Expo 或全部 Axios adapter 行为。

```sh
npm --prefix sdk/frontend ci
cd sdk/frontend
npx playwright install --with-deps chromium
npm run verify
```

浏览器测试启动临时本地服务，验证跨域对象传输以及同源／跨源 HttpOnly Cookie 会话恢复；消费测试打包后在临时目录安装 tarball，检查真实 ESM 与 TypeScript 导出。源码测试不能代替生产对象存储、CORS、会话服务和部署环境验收。
