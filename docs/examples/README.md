# 完整示例与验证索引

源码是示例的唯一维护位置；Markdown 中的关联代码块由 `make docs-check` 比较。场景片段需要的 DTO、权限和业务回调在各指南说明。

| 组件 | 完整源码 | 验证程度与入口 |
| --- | --- | --- |
| 前端 HTTP | [quickstart.ts](../../sdk/frontend/examples/quickstart.ts) | 隔离 tarball 双模式类型检查、本地 HTTP 实际请求；`make frontend-consumer` |
| 前端认证与 SSE | [auth-and-sse.ts](../../sdk/frontend/examples/auth-and-sse.ts) | 本地 401／刷新／终态消费；同上；完整浏览器行为由 `make frontend-browser-test` 回归 |
| Go 环境变量 | [Example](../../sdk/go/envconfig/example_test.go) | 设置并恢复测试环境变量，执行稳定输出；`make go-test` |
| Go JSON 与 server | [JSON Example](../../sdk/go/http/jsonbody/example_test.go)、[server Example](../../sdk/go/http/server/example_test.go) | 执行解码／构造与关闭，不代表生产监听验证；`make go-test` |
| Go Gateway | [Example](../../sdk/go/gateway/example_test.go) | 本地代理请求与资源关闭；`make go-test` |
| Go Session | [Examples](../../sdk/go/gateway/sessionauth/example_test.go) | KeyBuilder 实际执行；Store 装配只编译，真实 Redis 用 `make integration-session` |
| Go Logging | [Example](../../sdk/go/logging/example_test.go) | 实际 JSON 脱敏输出；`make go-test` |
| Go Kafka | [Example](../../sdk/go/mq/kafka/example_test.go) | 编译；实际执行需 Kafka 与既有 Topic，本仓库不自动安装 Kafka |
| Go Object Storage | [Example](../../sdk/go/objectstorage/s3store/example_test.go) | 编译；本地 MinIO 链路用 `make integration-storage`，真实 AWS 另有显式入口 |
| Python Logging | [example_logging.py](../../sdk/python/logging/tests/example_logging.py) | Ruff／mypy、实际 JSON 输出；`make python-logging-check python-logging-test` |
| Python Storage | [example_storage.py](../../sdk/python/storage/tests/example_storage.py) | Ruff／mypy、HTTPX 替身验证同步上传／异步下载及连接关闭；`make python-storage-check python-storage-test` |
| Storage 访问配置 | [storage-access.json](storage-access.json) | 现有契约测试验证 Schema；同 Python Storage 测试，不执行部署 |

Go 无 `Output` 的 Example 只编译验证，不连接外部依赖。有输出的本地示例、语言单元测试、容器集成和生产验收是不同层次，交付时分别列明。
