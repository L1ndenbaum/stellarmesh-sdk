# SDK 内容

## 目标与边界

`stellarmesh-sdk`提供轻量、可独立引用的基础组件。SDK不读取业务项目settings、请求上下文或部署目录；项目相关信息通过标准库、公开配置或可注入接口提供。

仓库不拥有Docker Compose、生产地址、Secret、Bucket、Policy、Kafka Topic、ClickHouse database、日志表、保留策略或生产迁移编排。这些资源由业务项目和`server-infrastructure`管理。

## 目录与制品

| 路径 | 内容 | 发布形式 |
| --- | --- | --- |
| `sdk/go/` | 环境配置、JSON请求解码与HTTP server基础能力 | Go Module |
| `sdk/go/objectstorage/` | namespace绑定的对象模型与S3适配器 | 独立Go Module |
| `sdk/go/gateway/` | 声明式Gateway、JWT、Redis限流与通用`slog`访问日志 | 独立Go Module |
| `sdk/go/logging/` | `slog.Handler`安全装饰器 | 独立Go Module |
| `sdk/go/mq/kafka/` | Kafka连接、Publisher、Topic检查和TLS/SASL | 独立Go Module |
| `sdk/python/logging/` | Python标准库安全单行JSON Formatter | `stellarmesh-logging` |
| `sdk/python/storage/` | Storage v1同步与异步客户端 | `stellarmesh-storage` |
| `contracts/logging/sanitization.md` | 轻量字段清洗约定与跨语言样例 | 随仓库版本 |
| `contracts/storage/v1/` | Storage控制面OpenAPI、Schema与共享限制 | 随仓库版本 |
| `services/storage/` | 项目级预签名控制面 | GHCR镜像 |
| `contracts/logging/v1/`、`contracts/logging/v2/` | 冻结的旧远程日志契约 | 只读历史 |

已经退役的公共logging-service、ClickHouse sink、迁移镜像和Gateway Logging Adapter只存在于`0.2.0`历史tag及其不可变制品中，不再位于主干或未来发布矩阵。

## 轻量日志组件

Go包装饰项目已有`slog.Handler`，Python包提供`logging.Formatter`。两者按[共同清洗约定](../contracts/logging/sanitization.md)处理支持类型、精确敏感字段匹配以及共享节点和深度预算；不会统一Go/Python的完整项目Schema，也不会定义Event、Topic、service token、异步队列或ClickHouse表。

推荐路径：

```text
标准库Logger
  -> 单行结构化JSON stdout/stderr
  -> 项目或节点级Collector
  -> 少量项目字段映射
  -> 项目自己的数据库表
```

项目可以自由增加metadata或顶层字段。为了便于通用采集，建议至少稳定提供时间、级别、消息、logger/service，但这不是跨项目wire契约。Collector的磁盘buffer必须有界、可重启恢复并暴露容量和drop指标；数据库sink按at-least-once设计，允许重复。

普通日志不承诺业务调用成功即持久化。需要与业务事务原子绑定的审计事件必须使用业务审计表或transactional outbox，不能通过提高日志级别获得合规保证。

## Gateway

Gateway使用固定安全顺序执行路由、可信代理解析、认证、授权、限流、转发策略和upstream解析。安全组件错误时fail-close；Observer和访问日志失败是旁路故障，不改变已完成响应。

Gateway默认把通用访问记录写入当前`slog.Default()`。项目可以调用`slog.SetDefault`选择JSON/text、级别和输出流，也可以注入`AccessLogger`或使用`WithoutAccessLog`关闭。Gateway Core不知道Collector、远程日志服务、Kafka、spool或数据库。

## 对象存储

`sdk/go/objectstorage`适合持有项目凭据的Go进程直接访问S3或MinIO。Storage v1控制面由项目级storage-service持有凭据，Python等客户端只取得预签名请求，对象字节不经过Go服务。

```text
Go服务 -> objectstorage -> S3/MinIO

Python或其他客户端
  -> storage-service认证、授权、readiness和预签名
  -> S3/MinIO直传对象字节
```

业务请求只使用逻辑namespace和key，不直接传Bucket。真正权限由IAM/MinIO Policy限制。storage-service不创建Bucket，不持有管理员凭据，不在启动时运行迁移。

## Kafka

独立Kafka Module只提供连接、PLAIN/SCRAM/TLS、要求全副本确认的Publisher和并行Topic检查。项目自己拥有Topic、ACL、consumer group、offset、提交、重试和幂等语义。Kafka是按需能力，不是Logging默认依赖。

## 版本兼容

- Go父SDK、Gateway、Logging、Kafka和Object Storage分别发布；
- Python Logging与Storage分别发布；
- `stellarmesh-logging 0.3.0`与Go Logging `v0.3.0`是破坏性轻量版本，不兼容旧远程API；
- 主干准备 Logging `0.4.0`，收窄隐式类型展开并改变匹配与 panic 策略；发布前不能按新版本从公共仓库安装；
- 冻结的Logging v1/v2契约只供仍运行`0.2.0`的项目迁移；
- Storage v1的OpenAPI、Schema、服务和Python客户端仍须保持契约测试一致；
- 已经推送的tag、PyPI包和GHCR镜像永不覆盖或移动。
