# 前端 HTTP SDK

`@stellarmesh/sdk` 提供基于 Axios 的可复用 HTTP 客户端和基于 Fetch 的 SSE 流。源码版本为 `0.3.0`，采用 MIT 许可证。业务 API、公共 API 和对象存储共用实现，通过独立实例配置。

安装：

```sh
npm install @stellarmesh/sdk --registry=https://registry.npmjs.org/
```

```ts
import { http, flattenEnvelopeResponse } from '@stellarmesh/sdk';

const api = http
  .withBaseURL('/api/v1')
  .withTimeout(15_000)
  .withMaxRetries(2)
  .withResponseTransform(flattenEnvelopeResponse());

const requestCreatePatient =
  api.post<{ name: string }, { id: number }>('/patients');

const patient = await requestCreatePatient({ name: '示例' });
```

`withXxx()` 返回新实例，必须接住返回值。默认无鉴权、无响应转换、不重试、不设置超时。`http` 是唯一声明根入口：`get/head/delete<TQuery, TResponse>` 的调用输入作为查询参数，`post/put/patch<TBody, TResponse>` 的调用输入作为请求体；输入为 `void` 时可无参调用。声明时不发送请求，每次调用独立执行；返回类型表示转换后的数据，泛型不校验服务端 DTO 字段。

声明方法的第二个参数是默认配置，返回函数的第二个参数是本次调用配置，优先级为调用配置、声明配置、客户端配置、SDK 默认值。headers 按大小写不敏感名称合并；`signal` 只在调用阶段提供。无输入接口可写 `requestWorkspace()`，携带配置时写 `requestWorkspace(undefined, { signal })`。`withMetadata()` 派生的声明返回 `HttpResponse<TResponse>`；通用 `request<TInput, TResponse>(resolve, defaults)` 在调用时同步组装方法、URL、查询和请求体，不提供立即发送入口。

`HttpMethod`、`ResponseType`、`AuthRefreshResult` 和 `HttpErrorKind` 同时提供运行时常量与同名类型，例如 `HttpMethod.GET`、`ResponseType.JSON`、`ResponseType.ARRAYBUFFER` 和 `HttpErrorKind.HTTP`。`HttpErrorKind.RESPONSE_FORMAT` 与 `HttpErrorKind.SESSION_CHANGED` 分别对应原有的 `response-format` 和 `session-changed` 错误值。它们使用 `as const` 对象及派生联合类型，原有字符串字面量和 `import type` 用法继续兼容。

`withErrorCodeExtractor(extractor)` 可单独配置 HTTP／业务错误的 `apiCode`，例如从四字段响应 `{ code, message, data, error_code }` 读取额外错误码；`error.status` 始终保留真实 HTTP 状态。未配置时继续使用原有 `code`，不要求项目升级信封结构：

```ts
import type { ErrorCodeExtractor } from '@stellarmesh/sdk';

const extractErrorCode: ErrorCodeExtractor = data => {
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

配置随不可变派生保留，在每次失败响应进入认证判断与重试前调用一次。字符串、数字（包括 `0`）原值保留；`null`／`undefined` 清空 `apiCode`，不回退到 `code`。回调第二个参数提供只读的真实状态与响应头。嵌套结构可在回调中自行读取，例如 `data.error.reason`，SDK 不定义字段名或业务错误码；`ApiEnvelope<T>` 继续只有 `code`、`message`、`data`。

提取不改变成功判定、消息或响应体；成功响应、网络／超时／取消／会话变化和认证回调错误不运行提取。`responseMode: 'raw'` 仍提取 HTTP 错误码。回调必须同步：抛错、非法返回值或 Promise／thenable 均产生 `HttpErrorKind.RESPONSE_FORMAT`，保留原状态、响应体与响应头，以 `cause` 保留异常或类型错误，不触发恢复或重试。

`createAuthSession` 创建显式共享会话，`withAuth(auth)` 装配后，普通派生继续共享刷新状态。业务方通过 `getAuthHeaders({ epoch })` 提供 Bearer、自定义认证头或 CSRF 头；Cookie Session 可省略该回调。`shouldRefresh` 和 `refreshSession` 必须成对配置，不提供默认 401 判断，不完整组合在类型检查和运行时均被拒绝；未启用刷新时不能配置 `onUnauthorized`。

刷新回调完成凭证保存后返回 `AuthRefreshResult.REFRESHED`，SDK 重新读取认证头并最多重放一次；确认不可恢复时返回 `AuthRefreshResult.EXPIRED`。刷新异常直接传播，不通知退出。必需的 `getSessionEpoch` 配合项目条件保存与清理，防止旧请求跨账号恢复。

浏览器跨源 Cookie 在绑定中显式配置 `.withAuth(auth, { withCredentials: true })`，仅对可信来源生效；Node 不提供 Cookie 容器。`authRecovery: false` 保留凭证携带并关闭认证恢复，适用于登录和刷新接口；`auth: false` 关闭 SDK 认证行为，但不禁止浏览器默认同源 Cookie。SDK 不自动生成 XSRF 头，业务方需显式提供。旧 `getAccessToken` 和刷新返回 Token／`null` 的契约已移除。

详细行为、鉴权与对象传输示例见仓库[前端 SDK 接入教程](https://github.com/L1ndenbaum/stellarmesh-sdk/blob/dev/docs/sdk/frontend/README.md)。打包制品不包含该仓库文档，可通过源码仓库查看。

## SSE 流式接口

`http.sse` 提供独立的 Fetch 流执行路径，普通 HTTP 和上传仍使用 Axios。配置从同一个不可变客户端继承，业务事件 DTO、JSON 解码、任务完成判定和页面状态由项目维护。

```ts
import { http, HttpMethod } from '@stellarmesh/sdk';

const api = http.withBaseURL('/api/v1').withTimeout(15_000);
const requestEvents = api.sse.get<{ topic: string }>('/events');
const requestGenerate = api.sse.post<{ prompt: string }>('/generate');
const requestTaskEvents = api.sse.request<{ id: string }>(input => ({
  method: HttpMethod.GET,
  url: `/tasks/${encodeURIComponent(input.id)}/events`,
}));

const controller = new AbortController();
try {
  for await (const message of requestGenerate(
    { prompt: '示例' },
    { signal: controller.signal },
  )) {
    // 字符串数据如何解码、何时 break，由业务协议决定。
    console.log(message.event, message.id, message.data, message.retry);
  }
} catch (error) {
  // HttpClientError 沿用 HTTP／网络／超时／取消／会话变化等错误类别。
  console.error(error);
}
```

- `get<TQuery>` 输入为查询参数；`post<TBody>` 输入作为 JSON 请求体，并允许配置额外 `params`。`request<TInput>` 同步组装 GET／POST 描述，查询参数仅来自描述。`void` 输入可以省略。首次迭代才执行映射、捕获 epoch 并发出请求；输入在开始消费前应保持稳定。
- 返回 `AsyncIterable<SseMessage>`，不返回 Promise。同一返回对象只消费一次，重复遍历不会再发请求；再次调用声明函数会创建新流。`break`、消费方抛错、取消和读流失败都会释放 reader 与连接。
- `SseMessage` 的 `data` 保留协议数据字符串；`event` 默认是 `message`，`id` 沿用最近的有效事件 ID，`retry` 为最近的有效非负安全整数。空 `id` 重置 ID，非法 `retry` 被忽略；二者不触发重连，也不会自动发送 `Last-Event-ID`。
- UTF-8、BOM、CRLF／CR／LF、多行 `data` 和注释遵循 SSE 分帧规则；只去掉冒号后的一个可选空格，不额外裁剪数据。EOF 不会补发缺少空行终止符的事件。SDK 正常报告流结束，不把 `[DONE]`、`turn_completed` 等解释为业务完成。
- headers、timeout、auth 和 authRecovery 采用调用配置、声明配置、客户端配置的优先级；认证回调头仍具有最高优先级。`signal` 只允许放在调用配置中。`timeout` 仅限制每次发出请求至收到响应头，默认 0；流开始后没有自动空闲超时，使用 `AbortSignal` 取消。
- SSE 忽略客户端普通重试和响应转换配置，不开放单次重试、进度、`responseType` 或 `responseMode`。`withMetadata()` 不改变 SSE 的消息返回形态；`flattenEnvelopeResponse` 不处理成功事件流。
- 成功响应必须是 HTTP 200、`text/event-stream` 且有可读响应体，否则产生 `HttpErrorKind.RESPONSE_FORMAT`。HTTP 失败保留响应头与 JSON／文本错误体，并使用同一个错误码提取器。
- 只有成功建流前的 HTTP 错误可按业务 `shouldRefresh` 恢复一次；它与普通 HTTP 共享 AuthSession 刷新协调、通知去重和取消隔离。流中断、成功流中的错误事件、正常 EOF 均不会触发重放；即使没有收到首条事件，网络失败也不自动重发 POST。
- Bearer／Cookie、可信 origin、`auth: false` 和 `authRecovery: false` 沿用已有规则，不自动推断 CSRF；重定向直接失败，避免目标变更绕过可信来源检查。Cookie 自动管理只支持浏览器。需要重定向的服务应直接声明最终 SSE 地址。
- epoch 在异步边界及每次交付事件前检查；本版不增加会话变化订阅，空闲连接需要应用主动取消。账号切换不能撤回已发出的请求或后端副作用。项目应区分流结束与业务终态，例如保留部分结果并提示未完成任务中断。

## 代码组织

`sse/contracts.ts`、`sse/parser.ts` 和 `sse/client.ts` 就近维护流契约、协议解析与执行；`auth/request.ts` 是 HTTP 和 SSE 共用的单次认证边界，刷新代次仍由 `auth/session.ts` 所有。

`src/http/` 按功能归档，契约与对应实现放在同一目录：

```text
src/
├── index.ts
└── http/
    ├── api/
    │   ├── contracts.ts
    │   └── api.ts
    ├── auth/
    │   ├── contracts.ts
    │   └── session.ts
    ├── client/
    │   ├── contracts.ts
    │   └── client.ts
    ├── request/
    │   └── contracts.ts
    ├── response/
    │   ├── contracts.ts
    │   └── envelope.ts
    ├── retry/
    │   ├── contracts.ts
    │   └── policy.ts
    ├── transport/
    │   └── axios-transport.ts
    └── error/
        ├── contracts.ts
        ├── extract-error-code.ts
        ├── http-client-error.ts
        └── cancellation.ts
```

- `contracts.ts` 定义所属功能供其他模块依赖的接口；实现文件使用具体名称。只有契约的功能允许暂时只有一个文件，不预建空实现。
- `api` 定义可复用的声明式请求函数，绑定方法、路径和默认配置；调用时通过内部执行器执行，不重复实现传输、认证或重试。
- `request` 定义请求方法、请求头、进度和请求选项；`response` 定义响应读取方式、返回结构与转换接口。`HttpMethod`、`ResponseType` 的运行时常量与同名派生类型保持相邻。
- `client` 只负责内部请求执行，公开配置派生由 `api` 负责；`auth` 负责认证会话契约、工厂和刷新协调；`retry` 区分公开配置与内部策略；`transport` 承载 Axios 适配。
- 信封适配器的专属类型与实现共同放在 `response/envelope.ts`；错误类、构造选项及类型守卫共同放在 `error/http-client-error.ts`，取消和等待辅助函数放在 `error/cancellation.ts`。不要求每个功能都创建契约文件。
- 错误码提取契约及辅助实现归 `error/`；`api` 保存派生配置，`client` 在统一错误处理阶段应用，不在传输和信封路径各自维护一份配置。
- 契约不引用客户端、认证协调器或 Axios 实现；认证契约通过类型导入引用错误类。认证会话的品牌声明与公开接口放在一起，私有刷新状态留在实现内。
- 内部直接引用具体文件，保留 ESM `.js` 路径及 `import type`，不通过包根入口或功能目录的聚合入口引用自身。`src/index.ts` 是唯一公开入口，不提供内部子路径导出。

这里的契约属于前端 HTTP API；仓库根目录 `contracts/` 继续负责跨语言公共协议，不在功能目录重复定义共享协议。行为测试继续通过包根入口验证公开契约，不依赖内部文件布局。

## 代码排版

顶层 `interface`、`type`、函数、类、枚举及带声明的 `export` 前后保留一个空行；是否导出不改变这些声明的间距要求。连续 import、纯重导出、接口成员和函数内部语句不强制逐条插入空行，说明注释与对应声明保持相邻。

ESLint 的 `@stylistic/padding-line-between-statements` 负责检查和自动补齐空行，Biome 负责其余代码排版。`npm run format` 先运行 ESLint 自动修复，再执行 `biome format --write .`；`npm run check` 执行只读格式检查、ESLint 和 TypeScript 类型检查，也会拦截缺失空行的声明。Biome 的 linter 和 assist 均关闭，静态规则继续由 ESLint 维护。

格式配置集中在 `biome.json`：两空格缩进、80 字符目标行宽、LF 换行、单引号、保留分号，并在允许的多行结构末尾添加逗号。短函数调用可以保持单行，长调用由 Biome 自动决定换行；`expand: "auto"` 控制对象和数组布局，不保证最后一个对象参数展开时，整个调用的参数都逐行展开。

格式化覆盖本包内 Biome 支持的代码和 JSON 文件，排除 `dist/`、`node_modules/` 和打包制品。Markdown 由人工维护，不参与自动格式化和格式检查；不再保留 Prettier。编辑器格式化应使用项目的 Biome 配置，避免保存时使用其他格式化器覆盖结果。

## 本地开发

使用 Node 24 和 npm，在本目录执行：

```sh
npm ci
npx playwright install --with-deps chromium
npm run format
npm run verify
```

`verify` 包含格式、静态和类型检查、行为测试、构建、Chromium 实际 HTTP 验证，以及隔离目录内的 tarball 消费测试。浏览器测试使用临时本地 HTTP 服务，不访问生产服务。消费测试需访问 npm 安装 tarball 的运行时依赖。

提供 ESM JavaScript 和类型声明，不提供 CommonJS 入口。初版验证浏览器与 Node ESM 消费，不声称已验证 Expo 或所有 Axios 适配器。

## 发布准备与许可证

本包采用 [MIT 许可证](LICENSE)，许可证随 tarball 分发。包归属 npm 组织 `stellarmesh`，发布账号需要拥有该组织及包的发布权限。

在本目录运行 `npm run release:prepare`，完成检查、行为测试、干净构建和 Chromium 验证后，生成一份 tarball，并在独立目录验证这份 tarball 的元数据、类型与运行时行为。成功时保留 `.artifacts/` 下的制品和 `release.json`，记录版本、目标 registry 与 SHA-512／SHA-256 校验信息；该命令不发布 npm 包。

`npm run build` 会先删除旧 `dist/`。普通 `npm pack` 通过 `prepack` 自动干净构建，但不会替代完整发布验证；消费测试与 `prepack` 不互相调用。`npm run test:consumer -- /绝对路径/包文件.tgz` 可验证指定的已有制品，不重新构建或替换它。发布时应上传已经验证的 tarball。

首次发布、手动制品工作流和后续自动发布安排见[发布说明](https://github.com/L1ndenbaum/stellarmesh-sdk/blob/dev/docs/release.md#前端-http-sdk-首次-npm-发布)。
