# 前端 HTTP SDK 接入

前端包位于 `sdk/frontend/`，包名暂定为 `stellarmesh-sdk`，源码版本 `0.1.0`，尚未发布 npm。它封装 Axios 传输、响应处理、鉴权刷新、重试和对象字节传输，不包含项目 DTO、分页兼容、上传会话或页面状态。

## 配置和调用

```ts
import {
  httpClient,
  flattenEnvelopeResponse,
  type HttpClient,
} from 'stellarmesh-sdk';

const apiClient = httpClient
  .withBaseURL('/api/v1')
  .withHeaders({ 'X-Client': 'web' })
  .withTimeout(15_000)
  .withMaxRetries(2)
  .withResponseTransform(flattenEnvelopeResponse());

const patient = await apiClient.post<{ name: string }, { id: number }>(
  '/patients',
  { name: '示例' },
  { signal: controller.signal },
);

const page = await apiClient.get<{ items: unknown[] }>('/patients', {
  params: { page: 1 },
});

await apiClient.post<void>('/session/logout');
```

示例中的 `controller` 由调用方创建。接口是普通 TypeScript API，不读取环境变量、浏览器存储，不依赖 React、路由器或项目上下文。业务层可依赖结构化 `HttpClient` 接口并注入测试实现。

- `withBaseURL`、`withHeaders`、`withTimeout`、`withMaxRetries`、`withRetry`、`withAuth`、`withResponseTransform` 均返回新实例，不改变原对象；应复用配置完成后的实例。普通配置派生保留相同认证会话引用；只有显式装配另一 `AuthSession` 才更换刷新状态。SSR 必须按用户请求创建认证会话。
- `post<TRequest, TResponse>`、`put<TRequest, TResponse>`、`patch<TRequest, TResponse>` 接受请求体；无请求体时使用 `post<TResponse>(url)` 等重载。双泛型不支持省略其中一个。
- `get<TResponse>`、`head<TResponse>`、`delete<TResponse>` 不接受请求体。需要 DELETE 请求体时使用 `request<TRequest, TResponse>`。
- `request<TRequest, TResponse>` 接受 `{ method, url, data, ...options }`；无请求体可写 `request<TResponse>`。
- 请求选项支持 `params`、`headers`、`timeout`、`maxRetries`、`retryable`、`signal`、`auth`、`authRecovery`、`responseMode`、`responseType`、`onUploadProgress`、`onDownloadProgress`。
- 优先级为请求选项、实例配置、SDK 默认值。headers 按大小写不敏感名称合并；SDK 注入的 Bearer Token 覆盖同名请求头。
- `withResponseTransform` 替换当前响应转换器，不追加无限拦截器链。转换器可返回同步值或 Promise，两种响应入口均等待转换完成；取消请求停止等待，但不会强行终止转换器自身的任务。公共接口不暴露 Axios 实例。

请求方法和响应类型可使用运行时常量；同名类型由 `as const` 对象派生，保留原字符串字面量的兼容性：

```ts
import { HttpMethod, ResponseType } from 'stellarmesh-sdk';

const method: HttpMethod = HttpMethod.GET;
const responseType: ResponseType = ResponseType.JSON;
const data = await apiClient.request<Patient>({
  method,
  url: '/patients/1',
  responseType,
});
```

响应类型常量为 `JSON`、`TEXT`、`BLOB`、`ARRAYBUFFER`，对应值仍为 `json`、`text`、`blob`、`arraybuffer`。只引用类型时仍可使用 `import type`。

## 信封和响应类型

普通请求返回 `Promise<TResponse>`，其中 `TResponse` 指最终数据。未启用转换时返回 HTTP 响应体，不返回 AxiosResponse；启用信封处理后返回信封内的 `data`。

```ts
const client = httpClient.withResponseTransform(
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

需要 HTTP 信息时使用 `requestWithMetadata`，泛型顺序与 `request` 一致，返回 `Promise<HttpResponse<TResponse>>`。其形态为 `{ data, status, headers }`，headers 名称统一小写，响应转换仅作用于 `data`。

## 鉴权和并发刷新

```ts
import { createAuthSession, httpClient } from 'stellarmesh-sdk';

const auth = createAuthSession({
  getSessionEpoch: () => session.getEpoch(),
  getAccessToken: () => session.getAccessToken(),
  refreshSession: ({ epoch }) => session.refreshAndSave(epoch),
  shouldRefresh: error =>
    error.status === 401 && error.apiCode === 'ACCESS_TOKEN_EXPIRED',
  onUnauthorized: (error, { epoch }) => session.handleExpired(error, epoch),
});

const apiClient = httpClient.withBaseURL(apiBaseURL).withAuth(auth);
const slowApiClient = apiClient.withTimeout(60_000);
const otherApiClient = httpClient.withBaseURL(apiBaseURL).withAuth(auth);
```

`apiBaseURL` 和 `session` 由项目注入。三个客户端显式共享同一认证会话，配置仍各自独立。即使传入同一个配置对象，两次 `createAuthSession(options)` 也会创建两个独立会话。`withAuth` 只接受工厂返回的 `AuthSession`，不再接受旧回调配置对象。

`getSessionEpoch()` 为同步读取函数，返回字符串或数字。登录、退出、切换账号或替换登录会话时必须更换且不可复用 epoch；同一会话正常刷新 Token 不更换 epoch。SDK 在异步读取 Token 前记录快照，并在读取后、发送或重放前、刷新结果返回后及成功响应交付前复核。不属于当前 epoch 的请求抛出 `kind: 'session-changed'`，不跨账号重放，也不触发旧会话的退出通知。SDK 不主动中断已经发出的服务端操作。

**项目必须在实际保存或清理凭证时执行 epoch 条件检查。** 刷新和退出回调均收到 `{ epoch }`；异步存储需使用条件写入或锁，不能仅在异步保存开始前检查一次。SDK 的返回后检查无法撤销项目已经写入的旧凭证。刷新接口应使用无该恢复逻辑的独立客户端。

| 刷新结果或请求条件 | SDK 行为 |
| --- | --- |
| 返回非空 Token | 项目已完成条件保存，SDK 复核 epoch 后使用返回的 Token 恢复重放 |
| 返回 `null` | 项目确认会话无法恢复，调用 `onUnauthorized` 后抛出认证失败响应 |
| 抛出异常 | 直接传播错误，不通知退出，不作为业务传输失败重试 |
| 返回空字符串 | 抛出鉴权契约错误，不解释为会话失效 |
| 恢复重放后再次出现匹配的认证失败 | 不再次恢复，通知退出并抛出响应错误 |
| 谓词返回 `false`、未配置刷新或 `authRecovery: false` | 不恢复也不通知退出，交给调用方处理 |

已有 `HttpClientError` 原样保留，Axios 错误保留规范化后的类别、字段和 cause；普通回调异常以 `kind: 'auth'` 包装并保留 cause。网络、超时、服务暂时不可用及凭证保存失败不等于会话失效。项目负责把明确表示刷新凭证失效的响应转换为 `null`。

`shouldRefresh` 默认只匹配 HTTP 401；自定义谓词接收 HTTP 或信封业务错误，可识别 HTTP 200 信封中的认证失败。网络、超时、取消和响应格式错误不进入谓词；谓词抛出异常也不会触发退出。普通业务错误不重试。

同一 epoch 内，并发及迟到的旧请求共用它们所属刷新代次的 Promise 和结果，包括失败。刷新完成后新请求可进入新代次，暂时失败不会永久禁用恢复。退出通知在对应代次内去重；取消一个请求只停止它的等待和后续重放，不取消其他请求共享的刷新。

可信 origin 属于客户端装配配置，可通过 `.withAuth(auth, { trustedOrigins })` 指定。未指定时仅信任该客户端 baseURL 的 origin；无 baseURL 时浏览器使用当前 origin。不同客户端不会合并或扩大可信集合，普通派生保留原显式集合。跨 origin 请求和 `auth: false` 不注入 Token，也不参与会话快照、刷新或退出通知。无浏览器环境需绝对 baseURL，或绝对请求地址及显式可信 origin。

这些限制只约束 SDK 自动注入的 Token。调用方手动设置的 Authorization、Cookie、请求地址和服务器重定向仍由调用方与服务器负责；初版不提供浏览器跨站 Cookie 鉴权配置。会话对象不应作为 SSR 进程级单例。

## 项目会话适配器的条件提交示例

下面演示同步内存状态的完整提交边界。`refreshAndValidate` 由项目实现，使用对应 epoch 的刷新凭证调用独立刷新客户端，只把明确的凭证失效转换为 `null`，其余异常抛出。

```ts
import {
  createAuthSession,
  HttpClientError,
  type AuthSessionEpoch,
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
    getAccessToken: () => state.accessToken,
    refreshSession: async ({ epoch }) => {
      const accessToken = await refreshAndValidate(epoch);
      assertEpoch(epoch);
      if (accessToken === null) return null;
      if (!accessToken.trim()) throw new Error('刷新返回了空 Token');
      // 检查与写入之间没有 await；正常刷新保留原 epoch。
      state = { ...state, accessToken };
      return accessToken;
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

## 超时、重试和取消

默认 `timeout: 0`、`maxRetries: 0`。`withTimeout(ms)` 是每次 Axios 传输尝试的超时，不是独立 TCP/TLS 建连超时，也不包含刷新和退避等待。调用方可通过 `signal` 限制整个操作的生命周期。

```ts
const client = httpClient.withRetry({
  maxRetries: 2,
  baseDelayMs: 300,
  maxDelayMs: 10_000,
});

await client.get('/items', { signal: controller.signal });
```

开启后，默认仅 GET/HEAD 对网络错误、超时、HTTP 408、429、502、503、504 重试。`maxRetries: 2` 表示首次尝试外最多重试两次。使用指数退避与随机抖动，默认基础 300 ms、等待上限 10 s。合法 `Retry-After` 是最小等待时长；超过上限直接返回原错误，非法值忽略。

POST/PATCH/PUT/DELETE 默认不自动重试。调用方确认服务端幂等语义、签名有效和请求体可重放后，在单次请求设置 `retryable: true`。`retryable: false` 可关闭 GET 重试；`maxRetries` 可覆盖实例额度。

每个逻辑请求最多进行一次认证恢复重放；普通传输重试另行计数，整个逻辑请求共享原额度，认证恢复不重置该额度。例如配置一次普通重试后，允许业务发送经历 `401 → 认证恢复重放 503 → 普通重试 200`，共发送三次。SDK 使用原执行循环重放，不重新调用公开请求方法。

`retryable: false` 只关闭普通重试；`authRecovery: false` 只关闭认证恢复及退出通知，仍正常附带凭证。不可重放的请求体或特殊签名请求应关闭认证恢复。允许恢复的写接口必须保证认证失败发生在业务副作用之前；关键写操作应配合服务端幂等机制。需要业务请求最多两次发送时，将普通重试设为 `0`。

取消、会话变化、格式错误和通常的其他 4xx 不触发普通重试；信封业务错误只有显式命中认证谓词才恢复。谓词不匹配或关闭认证恢复不影响适用的普通传输重试。超时不能证明写入失败，SDK 不生成幂等键或代替服务端去重。

## 对象存储传输

对象存储从无鉴权的根对象创建独立实例，不继承业务实例的 headers 或信封处理。项目先获得预签名请求，再原样提供 URL、方法和必要 headers；对象字节不经过业务 API 客户端。

```ts
const storageClient = httpClient.withTimeout(60_000);

const response = await storageClient.requestWithMetadata<Blob, string>({
  method: 'PUT',
  url: signedUrl,
  data: chunk,
  headers: signedHeaders,
  responseType: 'text',
  signal: controller.signal,
  onUploadProgress: ({ loaded, total }) => updateProgress(loaded, total),
});

const etag = response.headers.etag;
const file = await storageClient.get<Blob>(downloadUrl, {
  responseType: 'blob',
  signal: controller.signal,
});
```

进度回调提供 `loaded` 和可选 `total`，不保证事件次数或固定频率。Blob、ArrayBuffer、FormData、文本均可作为适用的请求体；使用 FormData 时由运行时生成 multipart boundary，不手工写入不完整的 Content-Type。

浏览器读取跨域 `ETag` 需要对象存储 CORS 暴露该响应头。SDK 保留 ETag 原值，不剥除引号，不推测上传成功后的业务状态。预签名过期、上传会话、分片调度、合并确认和失败补偿属于项目层；重试不会重新签名 URL。

## 错误处理

```ts
import { isHttpClientError } from 'stellarmesh-sdk';

try {
  await apiClient.get('/items');
} catch (error) {
  if (isHttpClientError(error) && error.kind === 'canceled') return;
  throw error;
}
```

`HttpClientError` 提供 `kind`、`status`、`apiCode`、`data`、`headers` 和 `cause`。类别包括 `http`、`business`、`network`、`timeout`、`canceled`、`response-format`、`auth`、`session-changed`、`unknown`。原始响应和 cause 可能含有业务数据或 Axios 请求配置，不能未经清洗直接记录；SDK 不自动记录 URL、请求体或凭据。

## 未发布初版的认证接口迁移

旧 `AuthOptions` 替换为 `AuthSessionOptions` 和 `AuthBindingOptions`，装配从 `withAuth(options)` 改为 `withAuth(createAuthSession(options), bindingOptions)`。新增必需的 `getSessionEpoch`，将 `trustedOrigins` 移至第二个装配参数。多个客户端需要共享时，复用同一个工厂返回值。

项目需实现刷新和退出回调的 epoch 条件提交，不能把临时刷新失败转换为 `null`。旧的“派生实例刷新状态独立”约定已替换；认证恢复重放也不再只限传输层 HTTP 401。

## 从 XieHe 接入

- 把 `post<TResponse, TBody>` 调整为 `post<TBody, TResponse>`；有请求体但此前只写一个泛型的调用也需补齐。
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

浏览器测试启动临时本地服务并验证跨域对象传输；消费测试打包后在临时目录安装 tarball，检查真实 ESM 与 TypeScript 导出。源码测试不能代替生产对象存储、CORS、会话服务和部署环境验收。
