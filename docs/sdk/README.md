# SDK 接入教程

本目录按可独立发布的组件划分：

- [Go 父 SDK](go/README.md)：标准库 HTTP 与环境配置能力；
- [Go 对象存储 SDK](go/object-storage.md)：namespace绑定的进程内对象存储；
- [Go Gateway SDK](go/gateway.md)：声明式、fail-close网关与标准 `slog` 访问日志；
- [Go Logging SDK](go/logging.md)：零第三方依赖的 `slog.Handler` 安全装饰器；
- [Go Kafka SDK](go/kafka.md)：Kafka连接、Publisher与Topic检查；
- [Python Logging SDK](python/README.md)：标准库 `logging` 的安全单行JSON Formatter；
- [Python Storage SDK](python/storage.md)：通过storage-service获取预签名请求；
- [storage-service部署](../storage-service.md)：项目级对象存储控制面。

主干 Logging `0.4.0` 尚未发布，当前已发布版本仍为 `0.3.0`。升级前阅读[字段清洗约定](../../contracts/logging/sanitization.md)及语言教程中的迁移说明。

## 日志默认路线

Go和Python日志包只帮助项目安全地产生结构化日志，不发送HTTP、不持有service token、不创建后台线程，也不规定Kafka Topic、ClickHouse表或审计模型。

```text
Go log/slog 或 Python logging
  -> 单行结构化 JSON stdout/stderr
  -> 项目选择的 Vector 等 Collector
  -> 项目自己的字段映射和数据库表
```

项目负责配置标准库的输出目标和最低级别。Collector负责批量、持久buffer、恢复重放和数据库不可用时的有界积压；数据库Schema、保留策略、DML凭据和migration继续归项目所有。普通运行日志采用at-least-once语义，必须接受重复和有限buffer最终写满的边界。

`contracts/logging/v1`、`contracts/logging/v2` 与旧Logging `0.2.0`只为现有项目迁移冻结保留，不是新项目的协议依赖。事务性审计应写入业务数据库或transactional outbox，不能依赖普通stdout链路。

## 对象存储路线

持有项目对象存储凭据的Go进程可以直接使用`objectstorage` Module。Python或其他客户端通过项目级storage-service取得预签名请求，对象字节直接与S3/MinIO传输。Storage v1公开契约位于`contracts/storage/v1`，Bucket、Policy、CORS、Lifecycle、Secret和生产资源编排不属于SDK。

正式环境必须固定经过验证的Module版本、Python包版本和镜像digest。完整发布边界见[发布与版本引用](../release.md)。
