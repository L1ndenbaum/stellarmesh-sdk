# Gateway 源码维护

[贡献流程](../../CONTRIBUTING.md) · [接入入口](../sdk/go/gateway.md)

## 源码组织与维护

网关根目录保持同一个 `package gateway`，公开导入路径和调用方式不因文件归档改变。每项能力的类型、接口、函数适配器、`WithXxx` 装配选项和实现就近放置；新增扩展点时优先扩充所属功能文件，不重新引入集中式 `types.go`、`interfaces.go` 或独立契约子包。

下表路径均相对于 `sdk/go/gateway/`：

| 文件 | 职责 |
|---|---|
| `gateway.go` | 网关构造、固定安全顺序的请求流水线和流水线级 panic 恢复 |
| `options.go` | 装配基础设施、内部配置、重复配置和空值检查 |
| `route.go`、`proxy.go` | 路由声明与匹配、上游解析与反向代理 |
| `authentication.go`、`policy.go`、`rate_limit.go` | 身份认证与注入、授权及转发前策略、三类限流 |
| `client_ip.go`、`request_id.go`、`cors.go` | 可信客户端地址、请求 ID 和跨域策略 |
| `health.go`、`response.go` | 健康检查、健康成功响应和错误响应协议 |
| `access_log.go`、`slog_access_log.go`、`observer.go` | 访问日志生命周期、标准库日志实现和观测事件 |
| `credential.go` | Cookie、Bearer 和自定义凭证提取 |
| `context.go` | 内部上下文键；公开上下文读取函数归入各自功能文件 |
| `http_token.go`、`response_recorder.go` | HTTP token 校验和响应状态记录 |
| `doc.go` | 包说明与维护约定 |

`jwtauth/`、`sessionauth/` 和 `redislimit/` 保持独立适配器包，通过根包接口接入。请求的安全阶段顺序统一由 `ServeHTTP` 维护，不由文件顺序或装配选项顺序决定。

功能测试放在对应的 `*_test.go`；`gateway_test.go` 验证跨阶段流程，`options_test.go` 验证装配约束，`test_helpers_test.go` 只承载跨文件共用的测试构造辅助函数。跨功能测试按主要断言归档，避免复制测试或为了文件位置增加测试。

