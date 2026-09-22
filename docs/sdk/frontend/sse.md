# SSE 流式消费

[返回接入入口](README.md)。本文场景片段配合[完整示例](../../../sdk/frontend/examples/quickstart.ts)阅读；业务 DTO 与回调由项目提供。

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


## 可执行的认证与流示例

[完整源码](../../../sdk/frontend/examples/auth-and-sse.ts)使用虚构的内存会话和终态协议，由消费验证提供本地 `/refresh`、`/events` 服务执行。业务方需要替换刷新、凭证保存和终态判断；取消可通过调用阶段 signal 接入。

<!-- example: sdk/frontend/examples/auth-and-sse.ts -->
```ts
import {
  AuthRefreshResult,
  createAuthSession,
  http,
  isHttpClientError,
} from '@stellarmesh/sdk';

/** 演示服务需提供 POST /refresh 返回 token，以及 GET /events 返回 SSE。 */
export async function readEvents(baseURL: string): Promise<string[]> {
  const publicApi = http.withBaseURL(baseURL).withTimeout(5_000);
  const requestRefresh = publicApi.post<void, { token: string }>('/refresh');
  // 示例内存会话；实际项目应在登录、退出、切换账号时更换 epoch。
  const session = { epoch: 1, token: 'expired-example-token' };
  const auth = createAuthSession({
    getSessionEpoch: () => session.epoch,
    getAuthHeaders: () => ({ Authorization: `Bearer ${session.token}` }),
    shouldRefresh: (error) => error.status === 401,
    refreshSession: async ({ epoch }) => {
      const result = await requestRefresh();
      if (epoch !== session.epoch) throw new Error('会话已变化');
      session.token = result.token;
      return AuthRefreshResult.REFRESHED;
    },
  });
  const requestEvents = publicApi.withAuth(auth).sse.get<void>('/events');
  const controller = new AbortController();
  const result: string[] = [];
  try {
    for await (const message of requestEvents(undefined, {
      signal: controller.signal,
    })) {
      // 本示例服务使用 completed 事件；SDK 不赋予它终态含义。
      if (message.event === 'completed') return result;
      result.push(message.data);
    }
    throw new Error('生成中断：未收到业务终态');
  } catch (error) {
    if (isHttpClientError(error)) console.error(error.kind, error.status);
    throw error;
  } finally {
    controller.abort();
  }
}
```
<!-- /example -->
