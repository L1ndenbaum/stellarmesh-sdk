# 前端构建与源码维护

[通用贡献流程](../../CONTRIBUTING.md) · [文档规范](../documentation.md) · [用户指南](../sdk/frontend/README.md)

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
- 源码与测试使用无后缀的相对路径直接引用具体 TypeScript 文件，保留 `import type`，不通过包根入口或功能目录的聚合入口引用自身。`src/index.ts` 是唯一公开入口，不提供内部子路径导出。直接由 Node 执行的 JavaScript 脚本仍保留相对导入的文件扩展名。

这里的契约属于前端 HTTP API；仓库根目录 `contracts/` 继续负责跨语言公共协议，不在功能目录重复定义共享协议。行为测试继续通过包根入口验证公开契约，不依赖内部文件布局。

## 代码排版

顶层 `interface`、`type`、函数、类、枚举及带声明的 `export` 前后保留一个空行；是否导出不改变这些声明的间距要求。连续 import、纯重导出、接口成员和函数内部语句不强制逐条插入空行，说明注释与对应声明保持相邻。

ESLint 的 `@stylistic/padding-line-between-statements` 负责检查和自动补齐空行，Biome 负责其余代码排版。`npm run format` 先运行 ESLint 自动修复，再执行 `biome format --write .`；`npm run check` 执行只读格式检查、ESLint 和 TypeScript 类型检查，也会拦截缺失空行的声明。Biome 的 linter 和 assist 均关闭，静态规则继续由 ESLint 维护。

格式配置集中在 `biome.json`：两空格缩进、80 字符目标行宽、LF 换行、单引号、保留分号，并在允许的多行结构末尾添加逗号。短函数调用可以保持单行，长调用由 Biome 自动决定换行；`expand: "auto"` 控制对象和数组布局，不保证最后一个对象参数展开时，整个调用的参数都逐行展开。

格式化覆盖本包内 Biome 支持的代码和 JSON 文件，排除 `dist/`、`node_modules/` 和打包制品。Markdown 由人工维护，不参与自动格式化和格式检查；不再保留 Prettier。编辑器格式化应使用项目的 Biome 配置，避免保存时使用其他格式化器覆盖结果。

## 本地开发

使用 Node 24（至少 24.11）和 npm，在本目录执行；最低版本是 tsdown 的构建环境要求，不代表消费者必须使用相同 Node 版本：

```sh
npm ci
npx playwright install --with-deps chromium
npm run format
npm run verify
```

`verify` 包含格式、静态和类型检查、行为测试、构建、Chromium 实际 HTTP／SSE 验证，以及隔离目录内的 tarball 消费测试。浏览器测试从 `dist/index.js` 组装测试页面，使用临时本地 HTTP 服务，不访问生产服务；单独运行 `test:browser` 前需先构建。消费测试需访问 npm 安装 tarball 的运行时依赖，同时使用 NodeNext 与 Bundler 解析模式检查公开类型，两者均关闭 `skipLibCheck`，并验证 Node ESM 实际请求。

提供 ESM JavaScript 和类型声明，不提供 CommonJS 入口。初版验证浏览器与 Node ESM 消费，不声称已验证 Expo 或所有 Axios 适配器。

## 构建职责

`tsc --noEmit` 负责源码、测试和构建配置的类型检查，使用 ESNext 模块与 Bundler 模块解析；tsdown 从唯一入口构建 SDK 自身代码并打包声明，输出 `dist/index.js` 和 `dist/index.d.ts`。构建目标为 ES2022，使用中立平台配置，不压缩、不生成 sourcemap。源码仍按功能维护，发布目录不再逐文件对应源码目录。

Axios 保留为 npm 运行时依赖，不将实现或类型内联到 SDK；最终由消费者的运行环境或构建工具解析包入口。esbuild 仅用于将已经构建的 SDK 与依赖组装成浏览器测试页面。业务项目继续从 `@stellarmesh/sdk` 导入，不需要编译 SDK 源码。

tarball 验证要求制品只包含上述两个构建文件、`package.json`、`README.md` 与 `LICENSE`，并检查 Axios 外部导入。每次构建先清理旧输出，避免已删除的内部模块残留到发布包。

## 发布准备与许可证

本包采用 [MIT 许可证](../../sdk/frontend/LICENSE)，许可证随 tarball 分发。包归属 npm 组织 `stellarmesh`，发布账号需要拥有该组织及包的发布权限。

在本目录运行 `npm run release:prepare`，完成检查、行为测试、干净构建和 Chromium 验证后，生成一份 tarball，并在独立目录验证这份 tarball 的元数据、类型与运行时行为。成功时保留 `.artifacts/` 下的制品和 `release.json`，记录版本、目标 registry 与 SHA-512／SHA-256 校验信息；该命令不发布 npm 包。

`npm run build` 会先删除旧 `dist/`。普通 `npm pack` 通过 `prepack` 自动干净构建，但不会替代完整发布验证；默认消费测试先显式构建，再跳过脚本打包，避免构建日志混入打包的 JSON 输出。`npm run test:consumer -- /绝对路径/包文件.tgz` 可验证指定的已有制品，不重新构建或替换它。发布时应上传已经验证的 tarball。

首次发布、手动制品工作流和后续自动发布安排见[发布说明](https://github.com/L1ndenbaum/stellarmesh-sdk/blob/dev/docs/release.md#前端-http-sdk-首次-npm-发布)。
