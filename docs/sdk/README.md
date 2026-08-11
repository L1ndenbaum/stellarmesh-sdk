# SDK 接入教程

本目录按照仓库中可独立发布和被业务项目引用的 SDK 划分。当前提供两个 SDK：

- [Go SDK 接入教程](go/README.md)：对应 `sdk/go` Go module；
- [Python SDK 接入教程](python/README.md)：对应 `sdk/python` 中发布的 `stellarmesh-logging` 包。

两种 SDK 都负责在业务进程内构造规范日志事件，通过有界异步队列批量发送到 `logging-service`。SDK 不负责部署 `logging-service`、Kafka、ClickHouse，不执行迁移，也不读取业务项目的配置模块。

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

SDK 后台请求收到 `202` 时，表示 `logging-service` 已经获得 Kafka 全同步副本确认，或已经把批次原子提交到本地持久 spool；它仍不表示 ClickHouse 已完成写入。整条链路按 at-least-once 工作，故障重试可能产生重复事件。

业务进程退出前必须显式关闭 SDK 客户端并给出排空超时。队列满、事件非法、网络失败、响应不符合契约或关闭超时时，应通过 SDK 的 drop callback 接入业务项目自己的指标或本地降级日志。

