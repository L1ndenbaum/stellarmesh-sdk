# 接入 SDK

## 日志接入

主干正在准备 Logging `0.4.0`，当前公开版本仍为 `0.3.0`。新清洗行为见[共享约定](../contracts/logging/sanitization.md)；原生等级差异（如 Go `WARN` 与 Python `WARNING`）需要项目 Collector 显式映射。

新项目不部署Stellarmesh公共logging-service。应用使用Go `log/slog`或Python `logging`输出结构化单行JSON，项目再选择Vector等Collector持久化到自己的数据库或文件。

Go示例：

```go
base := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})
safe, err := stellarlogging.NewSanitizingHandler(base, stellarlogging.HandlerOptions{
    ContextAttrs: requestAttrs,
})
if err != nil {
    return err
}
slog.SetDefault(slog.New(safe).With("service", "project-api"))
```

Python示例：

```python
handler = logging.StreamHandler(sys.stdout)
handler.setFormatter(JSONFormatter(static_fields={"service": "project-api"}))
logger.addHandler(handler)
```

项目自己的Collector至少要明确：

- 只采集哪些容器、Pod或文件，避免自采集循环；
- JSON解析失败时保留还是丢弃纯文本记录；
- 数据库字段映射、目标表、migration和retention；
- 数据库不可用时的有界磁盘buffer、重启恢复和重放；
- buffer满、磁盘故障、sink失败、重复记录和告警策略；
- runtime DML凭据与migration管理员凭据分离。

stdout成功不是端到端持久确认。Collector常见数据库sink是at-least-once，故障恢复后可能重复。对于“业务事务成功必须同时留下审计记录”的需求，应在同一事务写audit表或transactional outbox，再异步投影。

Gateway `v0.3.0`默认通过当前`slog.Default()`输出访问日志。业务项目只需在composition root设置JSON Handler和级别，不需要额外Logging Adapter。

## 旧远程日志链路迁移

Logging Go/Python `0.2.0`、Gateway Adapter `0.2.0`以及三个Logging `0.2.0`镜像已经冻结。现有项目迁移时必须：

1. 先建立项目自己的Collector、shadow表和故障恢复演练；
2. 将应用日志改为标准库结构化stdout；
3. 比对shadow与旧链路的数量、字段、延迟和敏感数据；
4. 停止旧producer并排空Client、service spool、Kafka lag和DLQ；
5. 停止旧service和writer，再删除token、Topic、consumer group与旧表迁移职责；
6. 最后删除旧SDK/Adapter依赖和冻结协议配置。

不能在同一事件上长期双写旧链路和Collector，否则会产生两个事件标识、重复计数和难以解释的投递边界。可以按环境双轨或写入独立shadow表完成验证。

## 对象存储接入

Go服务如果可以持有项目IAM Role或MinIO项目凭据，直接使用[Go Object Storage SDK](sdk/go/object-storage.md)。Python或其他客户端使用[Python Storage SDK](sdk/python/storage.md)连接项目级storage-service。

storage-service部署前需要：

1. 由业务部署或`server-infrastructure`创建Bucket、最小权限Policy和项目凭据；
2. 用只读访问文件声明逻辑namespace、Bucket/Prefix映射、principal token与capability；
3. 给服务注入标准AWS配置链和访问文件，不把Bucket暴露给业务请求；
4. 等待`/health/ready`确认所有namespace可访问；
5. Python客户端只把service token发送给storage-service，数据面只发送预签名结果中的Method和Headers。

完整参数、权限和故障语义见[storage-service部署文档](storage-service.md)。Bucket创建、Policy、CORS、Versioning、Lifecycle、Secret和生产网络不属于SDK仓库。

## 发布引用

Go使用与目录匹配的Module tag，Python使用固定distribution版本，生产镜像使用manifest digest。不要用仓库分支、本地`replace`、`latest`或可变SemVer镜像tag作为生产身份。具体版本和发布流程见[发布与版本引用](release.md)。
