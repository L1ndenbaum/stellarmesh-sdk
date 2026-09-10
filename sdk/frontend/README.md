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

`HttpMethod` 和 `ResponseType` 同时提供运行时常量与同名类型，例如 `HttpMethod.GET`、`ResponseType.JSON`、`ResponseType.ARRAYBUFFER`。它们使用 `as const` 对象及派生联合类型，原有字符串字面量和 `import type` 用法继续兼容。

`createAuthSession` 创建显式共享会话，`withAuth(auth)` 装配后，普通派生继续共享刷新状态；必需的 `getSessionEpoch` 配合项目条件保存与清理，防止旧请求跨账号恢复。刷新异常直接传播，只有明确失效或恢复重放仍失败才通知退出。`authRecovery: false` 可保留凭证注入并关闭认证恢复。

详细行为、鉴权与对象传输示例见仓库[前端 SDK 接入教程](../../docs/sdk/frontend/README.md)。打包制品不包含该仓库文档，可通过源码仓库查看。

## 代码组织

`src/http/` 按功能归档，契约与对应实现放在同一目录：

```text
src/
├── index.ts
└── http/
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
        ├── http-client-error.ts
        └── cancellation.ts
```

- `contracts.ts` 定义所属功能供其他模块依赖的接口；实现文件使用具体名称。只有契约的功能允许暂时只有一个文件，不预建空实现。
- `request` 定义请求方法、请求头、进度和请求选项；`response` 定义响应读取方式、返回结构与转换接口。`HttpMethod`、`ResponseType` 的运行时常量与同名派生类型保持相邻。
- `client` 负责客户端接口、配置派生和请求执行；`auth` 负责认证会话契约、工厂和刷新协调；`retry` 区分公开配置与内部策略；`transport` 承载 Axios 适配。
- 信封适配器的专属类型与实现共同放在 `response/envelope.ts`；错误类、构造选项及类型守卫共同放在 `error/http-client-error.ts`，取消和等待辅助函数放在 `error/cancellation.ts`。不要求每个功能都创建契约文件。
- 契约不引用客户端、认证协调器或 Axios 实现；认证契约通过类型导入引用错误类。认证会话的品牌声明与公开接口放在一起，私有刷新状态留在实现内。
- 内部直接引用具体文件，保留 ESM `.js` 路径及 `import type`，不通过包根入口或功能目录的聚合入口引用自身。`src/index.ts` 是唯一公开入口，不提供内部子路径导出。

这里的契约属于前端 HTTP API；仓库根目录 `contracts/` 继续负责跨语言公共协议，不在功能目录重复定义共享协议。行为测试继续通过包根入口验证公开契约，不依赖内部文件布局。

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
