# 对象传输与错误处理

[返回接入入口](README.md)。本文场景片段配合[完整示例](../../../sdk/frontend/examples/quickstart.ts)阅读；业务 DTO 与回调由项目提供。

## 可配置错误码提取

未配置时保持 `0.1.0` 行为，HTTP 错误从响应体 `code` 读取 `apiCode`，信封业务错误使用信封的 `code`。从 `0.2.0` 起，可以通过同步 `ErrorCodeExtractor` 独立决定错误码，保留 `error.status` 的真实 HTTP 状态：

```ts
import { http, flattenEnvelopeResponse } from '@stellarmesh/sdk';
import type { ErrorCodeExtractor } from '@stellarmesh/sdk';

const extractErrorCode: ErrorCodeExtractor = (data, context) => {
  // context.status 与 context.headers 是真实响应信息，不读取信封 code。
  if (!data || typeof data !== 'object') return undefined;
  const value = (data as Record<string, unknown>).error_code;
  return typeof value === 'string' || typeof value === 'number'
    ? value
    : undefined;
};

const client = http
  .withErrorCodeExtractor(extractErrorCode)
  .withResponseTransform(flattenEnvelopeResponse());
```

例如 HTTP 401 返回 `{ code: 401, message: '登录已过期', data: null, error_code: 'AUTH_ACCESS_TOKEN_EXPIRED' }`，错误的 `status` 为 `401`，`apiCode` 为字符串 `AUTH_ACCESS_TOKEN_EXPIRED`。这些字段名和错误码由项目约定；SDK 的 `ApiEnvelope<T>` 仍为三个字段，不要求额外字段。

嵌套协议只需换提取函数，不需要新增信封适配器：

```ts
const extractNestedCode: ErrorCodeExtractor = data => {
  if (!data || typeof data !== 'object') return undefined;
  const error = (data as Record<string, unknown>).error;
  if (!error || typeof error !== 'object') return undefined;
  const reason = (error as Record<string, unknown>).reason;
  return typeof reason === 'string' || typeof reason === 'number'
    ? reason
    : undefined;
};

const nestedClient = http.withErrorCodeExtractor(extractNestedCode);
```

- 配置只属于派生入口；父入口及其他分支不受影响，后续认证、metadata 等派生保留该配置。再次配置会替换当前提取器，不提供单次请求覆盖。
- 每次请求尝试产生带有响应状态的 `HttpErrorKind.HTTP` 或 `HttpErrorKind.BUSINESS` 错误时统一提取一次，先于 `shouldRefresh` 和普通重试判断。传入完整的原响应体；业务失败仍传入整个信封，第二个参数提供只读的真实状态与响应头。
- 字符串、数字（包括 `0`）原值写入 `apiCode`；返回 `null`／`undefined` 表示没有额外错误码，清空 `apiCode`，不回退到 `code`。提取不改变错误类别、消息、数据、响应头和 cause，不原地修改已有错误对象。
- 提取不决定请求成败，`flattenEnvelopeResponse` 的 `isSuccess` 仍只判断信封 `code`。HTTP 200 内的失败信封可以提取错误码，但带错误码字段的成功响应仍按原成功策略返回，不执行提取。
- 网络、超时、取消、会话变化、响应格式错误和认证回调自身错误不执行提取；无响应状态的自定义错误也不执行。`responseMode: 'raw'` 跳过成功响应转换，但 HTTP 错误仍执行提取；文本和二进制错误保留原始数据，提取器应先检查类型。
- 回调必须同步。抛出的异常保留为 `cause`；非法返回值（含 Promise／thenable）以 `TypeError` 为 `cause`。两者都产生 `HttpErrorKind.RESPONSE_FORMAT`，保留原响应状态、数据与响应头，不进入认证恢复或自动重试。SDK 会观察误传异步结果的拒绝，避免未处理的 Promise 拒绝，但不会等待结果。

需要将特定错误交给已有认证恢复时，在项目的 `shouldRefresh` 中判断 `error.status` 和提取后的 `error.apiCode`。这项配置只提供判断依据，不新增刷新接口、错误码枚举或全局消息通知。

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
import { HttpErrorKind, isHttpClientError } from '@stellarmesh/sdk';

try {
  const requestItems = apiClient.get<void, Item[]>('/items');
  await requestItems();
} catch (error) {
  if (isHttpClientError(error) && error.kind === HttpErrorKind.CANCELED) return;
  throw error;
}
```

`HttpErrorKind` 同时导出 `as const` 常量和同名联合类型，可用 `HttpErrorKind.HTTP` 比较错误类别，原有字符串和 `import type` 继续有效。`RESPONSE_FORMAT`、`SESSION_CHANGED` 成员分别对应 `response-format`、`session-changed` 字符串值。

`HttpClientError` 提供 `kind`、`status`、`apiCode`、`data`、`headers` 和 `cause`。类别包括 `http`、`business`、`network`、`timeout`、`canceled`、`response-format`、`auth`、`session-changed`、`unknown`。原始响应和 cause 可能含有业务数据或 Axios 请求配置，不能未经清洗直接记录；SDK 不自动记录 URL、请求体或凭据。

