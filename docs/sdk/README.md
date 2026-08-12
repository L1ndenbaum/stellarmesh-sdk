# SDK 接入教程

本目录按照仓库中可独立发布和被业务项目引用的 SDK 划分。当前提供两个语言 SDK：

- [Go SDK 接入教程](go/README.md)：对应 `sdk/go` Go module，包含日志、HTTP 和网关能力；
- [Python SDK 接入教程](python/README.md)：对应 `sdk/python` 中发布的 `stellarmesh-logging` 包。

两种语言 SDK 都能在业务进程内构造规范日志事件，通过有界异步队列批量发送到 `logging-service`。Python SDK 还提供标准库 `logging.Handler` 适配器，使已有项目无需替换日志调用方式。Go SDK 另外提供[声明式网关组件](go/gateway.md)，项目通过 `WithXxx` 选择路由、鉴权、限流和观测能力，安全执行顺序由 SDK 固定。SDK 不负责部署 `logging-service`、Kafka、ClickHouse，不执行迁移，也不读取业务项目的配置模块。

## 共同准备

接入任一 SDK 前，需要从业务项目自己的配置与 Secret 管理中取得：

- `logging-service` 的 HTTP 地址，例如 `http://logging-service:8091`；
- 分配给当前业务 `service` 的 token；
- 稳定、唯一的 `service` 名称；
- 当前项目使用的 SDK 固定版本。

token 与 `service` 必须匹配。SDK 会把 token 放入 `X-Logging-Service-Token` 请求头，`logging-service` 会拒绝 token 无权代表的 `service`。

正式环境必须使用已经发布的固定版本，不要直接引用可变分支。Go 与 Python SDK、`logging-service` 镜像应来自同一个发布 commit；完整发布规则见[发布与版本引用](../release.md)。

## 语义边界

业务线程调用 logger 时只把事件放入 SDK 的本地有界队列，不等待网络请求。因此 logger 返回 `true` 仅表示成功入队，不表示已经到达 `logging-service`。

SDK 会对网络异常和少数临时 HTTP 状态执行有限次数重试，所有尝试复用原 `event_id`。超过重试上限后事件会交给 drop callback，SDK 不提供本地持久队列，因此“业务进程到 logging-service”属于 best-effort，不能宣称整条链路都是 at-least-once。后台请求收到合法 `202` 后，表示 `logging-service` 已经获得 Kafka 全同步副本确认，或已经把批次原子提交到本地持久 spool；从该持久点到 sink 按 at-least-once 工作，仍可能产生重复事件，也不表示 ClickHouse 已完成写入。

业务进程退出前必须显式关闭 SDK 客户端并给出排空超时。队列数量或累计序列化字节达到上限、事件非法、网络重试耗尽、响应不符合契约或关闭超时时，应通过 SDK 的 drop callback 接入业务项目自己的指标或本地降级日志。
