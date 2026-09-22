# 基础请求与响应

[返回接入入口](README.md)。本文场景片段配合[完整示例](../../../sdk/frontend/examples/quickstart.ts)阅读；业务 DTO 与回调由项目提供。

## 声明式接口

业务 API 先声明，再由 Application 调用。统一从 `http` 根对象派生配置，声明和配置过程都不发送网络请求：

```ts
// shared/api/http.ts
import {
  http as rootHttp,
  flattenEnvelopeResponse,
} from '@stellarmesh/sdk';

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
import { HttpMethod } from '@stellarmesh/sdk';

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

`withBaseURL`、`withHeaders`、`withTimeout`、`withMaxRetries`、`withRetry`、`withAuth`、`withResponseTransform`、`withErrorCodeExtractor` 和 `withMetadata` 均返回新声明入口，必须接住返回值。认证会话引用在普通派生中共享，显式更换会话时才改变；不同入口不会合并可信 origin。

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

