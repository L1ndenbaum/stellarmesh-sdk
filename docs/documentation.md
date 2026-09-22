# 文档、注释与示例维护规范

## 信息只维护一次

| 信息 | 权威位置 | 其他入口的职责 |
| --- | --- | --- |
| 组件选择 | `docs/sdk/README.md` | 根 README 提供阅读路径 |
| 当前已发布版本、发布操作 | `docs/release.md` | 安装示例标注适用版本，链接矩阵，不复制状态表 |
| 历史版本与校验值 | `docs/releases/history.md` | 保持当时事实，不按当前状态改写 |
| 组件关系、责任边界 | `docs/sdk-content.md` | 跨组件指南讲装配流程 |
| 使用方法 | 组件 README、`docs/sdk/` | README 完成首次调用，任务指南解释场景 |
| 协议、清洗规则 | `contracts/` | 指南引用定义，不复制完整规范 |
| 构建、布局、验证 | `CONTRIBUTING.md`、对应维护文档 | 接入指南提供链接 |

独立发布组件的 README 顺序为用途与前置条件、安装、最小完整示例、关键限制、深入指南。npm／PyPI README 的深入链接使用仓库绝对 URL。保留旧文档入口与必要锚点，用导航指向新位置。Markdown 自然语言使用中文，代码标识和工具术语保持原文；不启用 Markdown 自动格式化。

## 公共注释

先说明用途，再说明类型签名表达不了的约束。按需解释默认值、单位、覆盖关系、错误与拒绝的区别、资源归属、并发和副作用，不要求每个声明填满模板。

- TypeScript 使用 JSDoc 摘要，必要时加 `@param`、`@returns`、`@remarks`、`@example`。说明声明与执行、认证恢复、单次流消费和取消语义，避免把业务 DTO 或终态写成 SDK 契约。
- Go 包说明以 `Package 包名` 开头，导出声明注释以声明名开头。配置字段说明零值、单位和有效范围。使用外部测试包的 `Example*`；有稳定 `Output` 的例子执行验证，没有输出断言的外部依赖装配例子只编译验证。
- Python 使用中文 Google 风格 docstring，按需要采用 `Args`、`Returns`、`Raises`、`Attributes`。Client／AsyncClient 说明关闭职责、请求失败、流和文件覆盖边界；继承的构造配置在公共类说明中可查，不为注释增加覆盖方法。
- 内部注释解释安全顺序、会话隔离、签名和释放资源的非显然约束，不重复赋值或分支，不机械覆盖私有函数。

注释必须由现有实现或测试支持。未验证的线程安全、自动重试、兼容性不写成承诺；发现行为缺陷单独记录。冻结的 Logging v1/v2 不改写。

## 完整示例与片段

完整示例以源码为准，包含公开 import、初始化、一次操作、错误处理与资源释放。业务回调、Redis／Kafka／S3 等前置条件必须说明。场景片段明确标为片段并链接完整示例。

文档需要展示源码时，用以下标记包住一个代码块；路径相对于仓库根目录，内容与源文件一致（仅允许末尾换行差异）：

```text
<!-- example: path/to/example.ts -->
代码围栏与源码全文
<!-- /example -->
```

可使用 `path/to/example.ts#region` 引用命名片段，源码以 `// docs:start region` 与 `// docs:end region`（Python 使用 `#`）界定。标记本身不进入展示片段。CI 只校验，不改写 Markdown；更新示例时同步手工更新片段。

前端示例从包根导入，随 tarball 消费检查编译并连接本地受控服务。Python 示例通过公开入口导入并纳入静态检查与测试；Go 示例区分本地执行和外部依赖装配。部署命令只说明步骤，不由文档测试执行。配置示例使用虚构值，并通过现有 Schema 校验。

轻量 `make docs-check` 负责仓库内链接、锚点及标记片段的一致性，不把全网可用性作为必过检查。语言测试负责示例行为，制品检查负责公开注释在发布内容中保留；两者不能由链接检查代替。

## 参考实践

本规范借鉴 [Stripe 配置注释](https://github.com/stripe/stripe-node/blob/master/src/lib.ts)、[Supabase 公共方法说明](https://github.com/supabase/supabase-js/blob/master/packages/core/supabase-js/src/SupabaseClient.ts)、[go-redis 配置](https://github.com/redis/go-redis/blob/master/options.go)和 [HTTPX 客户端生命周期](https://www.python-httpx.org/advanced/clients/)。Go 格式以 [Go doc 规范](https://go.dev/doc/comment)和[可测试示例](https://go.dev/blog/examples)为依据，按本仓库规模采用必要部分。
