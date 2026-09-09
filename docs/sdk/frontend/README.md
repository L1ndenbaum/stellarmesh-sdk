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

- `withBaseURL`、`withHeaders`、`withTimeout`、`withMaxRetries`、`withRetry`、`withAuth`、`withResponseTransform` 均返回新实例，不改变原对象；应复用配置完成后的实例。派生实例拥有独立刷新状态，SSR 必须按用户请求隔离会话实例。
- `post<TRequest, TResponse>`、`put<TRequest, TResponse>`、`patch<TRequest, TResponse>` 接受请求体；无请求体时使用 `post<TResponse>(url)` 等重载。双泛型不支持省略其中一个。
- `get<TResponse>`、`head<TResponse>`、`delete<TResponse>` 不接受请求体。需要 DELETE 请求体时使用 `request<TRequest, TResponse>`。
- `request<TRequest, TResponse>` 接受 `{ method, url, data, ...options }`；无请求体可写 `request<TResponse>`。
- 请求选项支持 `params`、`headers`、`timeout`、`maxRetries`、`retryable`、`signal`、`auth`、`responseMode`、`responseType`、`onUploadProgress`、`onDownloadProgress`。
- 优先级为请求选项、实例配置、SDK 默认值。headers 按大小写不敏感名称合并；SDK 注入的 Bearer Token 覆盖同名请求头。
- `withResponseTransform` 替换当前响应转换器，不追加无限拦截器链。转换器可返回同步值或 Promise，两种响应入口均等待转换完成；取消请求停止等待，但不会强行终止转换器自身的任务。公共接口不暴露 Axios 实例。

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
const apiClient = httpClient
  .withBaseURL(apiBaseURL)
  .withAuth({
    getAccessToken: () => session.getAccessToken(),
    refreshSession: async () => {
      const token = await session.refreshAccessToken();
      // 项目负责保存新会话，当前请求使用返回的新 Token 重放。
      return token;
    },
    onUnauthorized: error => session.handleUnauthorized(error),
  });
```

`apiBaseURL` 和 `session` 由项目注入。`refreshSession` 返回新 access token 或 `null`，刷新接口本身使用无该刷新逻辑的独立客户端。回调抛出异常时保留 cause 并报告鉴权错误。

SDK 默认只向 baseURL 的 origin 注入 Token；无 baseURL 时，浏览器使用当前页面 origin。可用 `trustedOrigins` 显式替换可信 origin 集合。跨 origin 请求、协议相对的外部地址或 `auth: false` 不注入 Token，也不触发刷新。无浏览器环境需绝对 baseURL 或可解析的绝对请求地址及显式可信 origin。

这些限制只约束 SDK 自动注入的 Token。调用方手动设置的 Authorization、Cookie、请求地址和服务器重定向仍由调用方与服务器负责；可信 origin 应是项目实际控制的 API 源。初版不提供浏览器跨站 Cookie 鉴权配置。

同一实例并发 401 共享一次刷新，已经完成刷新后才到达的旧请求 401 复用该结果。每个逻辑请求最多刷新后重放一次；再次 401 不循环。最终未授权通知在同一刷新代次内只执行一次，回调应将应用切换到未登录状态。新请求可以开始下一次刷新尝试。

取消某个请求会停止其刷新等待和重放，不取消其他请求共享的刷新任务。刷新服务、Token 持久化、登录跳转及通知展示均归项目负责。

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

取消、信封业务失败、格式错误和通常的其他 4xx 不重试。普通重试额度在逻辑请求内共享，401 仅额外允许一次刷新重放，不重置额度。超时不能证明写入失败，SDK 不生成幂等键或代替服务端去重。

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

`HttpClientError` 提供 `kind`、`status`、`apiCode`、`data`、`headers` 和 `cause`。类别包括 `http`、`business`、`network`、`timeout`、`canceled`、`response-format`、`auth`、`unknown`。原始响应和 cause 可能含有业务数据或 Axios 请求配置，不能未经清洗直接记录；SDK 不自动记录 URL、请求体或凭据。

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
