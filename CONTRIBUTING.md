# 贡献与本地维护

先阅读[架构与责任边界](docs/sdk-content.md)及[文档、注释与示例规范](docs/documentation.md)。源码按功能归档，契约、配置、实现和测试就近组织，不引入集中承载类型的目录。

## 环境与验证

构建使用 Go 1.24、Python 3.11、uv、Node 24（最低 24.11）与 npm。Node 下限来自前端构建工具，不是给 SDK 消费者新增的运行时要求。容器集成需要 Docker；业务依赖的安装与生产部署由使用方负责。

```sh
make bootstrap
make format
make docs-check
make verify
make race
make images
make integration
```

`make bootstrap` 按两个独立 `uv.lock` 创建 Python 3.11 环境，并使用 Node 24、npm 安装前端锁定依赖和 Chromium。Linux 首次安装浏览器系统依赖可在 `sdk/frontend/` 执行 `npx playwright install --with-deps chromium`。`make verify` 会执行 Go 格式检查、`go vet`、Go 测试、两个 Python 项目的 Ruff、mypy、pytest、依赖兼容检查、前端格式／静态／类型检查、行为测试、构建、Chromium 传输和隔离 tarball 消费验证、Shell 语法检查与 `git diff --check`。`make race` 运行全部 Go 竞态检查。`make images` 构建 storage-service，`make integration-session` 使用隔离 Redis 验证会话与并发，接入方式见 [Redis Session 认证](docs/sdk/go/sessionauth.md)。`make integration` 同时执行 Session 集成，并验证 MinIO 最小权限、预签名直传、Multipart、版本删除、readiness 故障恢复和优雅关闭。测试结束后清理临时容器、网络和 Secret，不要求仓库提供 Compose。

前端单独开发使用 `make frontend-format` 与 `make frontend-verify`，接入方式见[前端 HTTP SDK](docs/sdk/frontend/README.md)，npm 制品准备见[发布说明](docs/release.md#前端-http-sdk-首次-npm-发布)。浏览器验证使用临时本地服务，消费验证需要 npm 依赖访问；运行验证前先完成 `make bootstrap`。

本地与 Python 发布流程的 mypy 检查各包的 `src/`、`tests/` 以及文档校验工具，构建后可以直接重新验证，无需删除 `build/` 或已有制品。


## 改动与发布

只运行与当前改动对应的检查；交付前执行 `make verify`。Go 注释与示例也经过 gofmt、go vet 和测试，Python docstring 与示例经过 Ruff、mypy 和 pytest。集成需要的 Redis、Kafka、S3 与浏览器验证应分别报告，不能用“编译通过”代替外部服务验收。

每项完成的改动以中文 conventional commit 提交，提交前执行 `git diff --check`。公开行为变化同时更新指南、IDE 注释及必要示例，协议和默认值以实现与契约测试为准。不要顺带修复文档整理中发现的运行时问题。

发布是单独操作，按[发布流程](docs/release.md)验证唯一制品、CI 和远端权限；本地构建、准备 tarball 或提交代码都不等同于发布。

前端源码布局、导入排版和 tsdown 制品职责见[前端维护指南](docs/contributing/frontend.md)。

## 文档与制品验证

`make docs-check` 仅使用 Python 标准库，离线检查受 Git 管理的 Markdown 链接、标题／显式锚点和关联源码片段，包含检查器自身的回归。`make go-doc-check` 确认关键配置可通过 go doc 查阅。前端消费测试检查安装后类型声明中的 JSDoc、编译文档示例并在本地 HTTP／SSE 服务上运行。

`make python-logging-artifact-check python-storage-artifact-check` 在临时目录构建 wheel／sdist、执行 twine 检查，并从 wheel 导入公开对象核对 help 信息与 README；结束后清理临时目录。验证已有制品可运行 `uv run --project sdk/python/storage --frozen python tests/docs/python_artifact.py sdk/python/storage /制品目录`，该形式不构建、不替换文件。这些检查均接入 `make verify` 与对应 CI 任务。

示例的验证入口见[示例验证索引](docs/examples/README.md)。所有网络行为测试使用受控服务或显式集成入口，不将静态验证表述为生产验收。
