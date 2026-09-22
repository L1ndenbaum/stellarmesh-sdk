# 前端 HTTP／SSE SDK

`@stellarmesh/sdk` 提供声明式 HTTP 与 SSE，适合浏览器和 Node ESM。普通 HTTP／上传基于 Axios，SSE 基于 Fetch；业务 DTO、会话存储与事件终态由项目维护。采用 [MIT](LICENSE)。

## 安装

以下示例适用于 `0.3.0`，当前已发布状态见[发布矩阵](https://github.com/L1ndenbaum/stellarmesh-sdk/blob/dev/docs/release.md#当前制品矩阵)。

```sh
npm install @stellarmesh/sdk@0.3.0
```

## 最小完整示例

将下面的函数放入应用，并传入提供 `GET /items` 的服务地址，例如 `await listItems('/api')`。服务应返回 `{ "items": ["示例"] }`；泛型描述约定，不执行 DTO 校验。示例源码由隔离 tarball 消费测试在本地 HTTP 服务上验证。

<!-- example: sdk/frontend/examples/quickstart.ts -->
```ts
import { http, isHttpClientError } from '@stellarmesh/sdk';

/** 服务需提供 GET /items，响应为 { items: string[] }。 */
export async function listItems(baseURL: string): Promise<string[]> {
  const requestItems = http
    .withBaseURL(baseURL)
    .withTimeout(5_000)
    .get<void, { items: string[] }>('/items');
  const controller = new AbortController();
  try {
    return (await requestItems(undefined, { signal: controller.signal })).items;
  } catch (error) {
    if (isHttpClientError(error)) console.error(error.kind, error.status);
    throw error;
  } finally {
    controller.abort();
  }
}
```
<!-- /example -->

## 关键限制

- 声明时不请求，调用时执行；`withXxx` 返回新实例，已有声明不受后续派生影响。
- 默认不重试、不设超时、不解包信封，也没有默认 401 刷新策略。恢复判断和刷新回调必须成对配置；Cookie 自动管理限浏览器。
- SSE 首次迭代才建连，同一对象只消费一次。`break` 会关闭流；断流与 EOF 不自动重连，也不代表业务完成。
- `signal` 只属于调用配置。SSE 超时只到响应头；普通 HTTP 超时按每次传输计时。不要跨用户共享绑定会话的入口。

## 深入指南

[请求与响应](https://github.com/L1ndenbaum/stellarmesh-sdk/blob/dev/docs/sdk/frontend/http.md)、[认证](https://github.com/L1ndenbaum/stellarmesh-sdk/blob/dev/docs/sdk/frontend/auth.md)、[SSE](https://github.com/L1ndenbaum/stellarmesh-sdk/blob/dev/docs/sdk/frontend/sse.md)、[对象传输与错误](https://github.com/L1ndenbaum/stellarmesh-sdk/blob/dev/docs/sdk/frontend/errors-and-transfer.md)、[迁移](https://github.com/L1ndenbaum/stellarmesh-sdk/blob/dev/docs/sdk/frontend/migration.md)。维护者阅读[构建约定](https://github.com/L1ndenbaum/stellarmesh-sdk/blob/dev/docs/contributing/frontend.md)。主干指南包含尚未发布的维护改进，版本行为以对应 tag 为准。
