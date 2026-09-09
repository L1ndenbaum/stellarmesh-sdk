# 前端 HTTP SDK

`stellarmesh-sdk` 提供基于 Axios 的可复用 HTTP 客户端。当前源码版本为 `0.1.0`，尚未发布到 npm。业务 API、公共 API 和对象存储共用实现，通过独立实例配置。

```ts
import { httpClient, flattenEnvelopeResponse } from 'stellarmesh-sdk';

const apiClient = httpClient
  .withBaseURL('/api/v1')
  .withTimeout(15_000)
  .withMaxRetries(2)
  .withResponseTransform(flattenEnvelopeResponse());

const patient = await apiClient.post<{ name: string }, { id: number }>(
  '/patients',
  { name: '示例' },
);
```

`withXxx()` 返回新实例，必须接住返回值。默认无鉴权、无响应转换、不重试、不设置超时。带请求体的方法采用请求泛型在前、响应泛型在后；返回类型表示转换后的数据。泛型不校验服务端 DTO 字段。

详细行为、鉴权与对象传输示例见仓库[前端 SDK 接入教程](../../docs/sdk/frontend/README.md)。打包制品不包含该仓库文档，可通过源码仓库查看。

## 代码排版

顶层 `interface`、`type`、函数、类、枚举及带声明的 `export` 前后保留一个空行；是否导出不改变这些声明的间距要求。连续 import、纯重导出、接口成员和函数内部语句不强制逐条插入空行，说明注释与对应声明保持相邻。

ESLint 的 `@stylistic/padding-line-between-statements` 负责检查和自动补齐空行，Prettier 负责其余排版并将多个连续空行压为一个。`npm run format` 先运行 ESLint 自动修复，再运行 Prettier；`npm run check` 会拦截缺失空行的声明。

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
