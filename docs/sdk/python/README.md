# Python Logging SDK 接入教程

`stellarmesh-logging==0.3.0`要求Python 3.11及以上，没有运行时第三方依赖。它只提供标准库`logging.Formatter`，不拥有Handler、stream、后台线程、远程服务或数据库。

## 安装

```sh
python -m pip install stellarmesh-logging==0.3.0
```

## 配置结构化stdout

```python
import logging
import sys

from stellarmesh_logging import JSONFormatter

handler = logging.StreamHandler(sys.stdout)
handler.setLevel(logging.INFO)
handler.setFormatter(
    JSONFormatter(
        static_fields={
            "service": "orders-api",
            "environment": "production",
        },
    )
)

logger = logging.getLogger("orders")
logger.setLevel(logging.INFO)
logger.addHandler(handler)
logger.info(
    "request completed",
    extra={"request_id": "request-1", "duration_ms": 12.5},
)
```

基础输出字段是`time`、`level`、`msg`和`logger`。`static_fields`适合service、environment等进程级稳定字段，`LogRecord.extra`适合项目自己的请求或业务字段。`time`、`level`、`msg`、`logger`、`source`和`exception`不能被覆盖。

`include_source=True`时增加文件、行号和函数。异常输出为`exception.type`、`exception.message`与`exception.traceback`；换行由JSON转义，因此一条记录始终只占一个物理行。输出使用紧凑UTF-8 JSON，并拒绝NaN和Infinity。

## 脱敏和限制

Formatter默认限制message和字符串为`16KiB`、项目属性为64项、嵌套深度为8。敏感key先转小写并移除非字母数字字符，再匹配password、secret、token、authorization、cookie、API key、client secret、private key等常见形式。`extra_sensitive_keys`只扩展默认集合。

敏感值输出`[REDACTED]`，不可序列化值输出`[UNSERIALIZABLE]`，截断值以`[TRUNCATED]`结尾。Formatter不会修改调用方的mapping或sequence，也不会扫描message内容；项目不得主动把Secret拼入日志消息。

日志等级由标准库Logger和Handler控制。Formatter不实现第二套`minimum_level`，也不会关闭调用方的stream。多进程或fork应用应在自己的进程初始化边界安装Handler，避免重复注册。

## Collector与迁移

推荐把单行JSON写stdout，再由项目自己的Vector等Collector完成解析、有界磁盘buffer、恢复重放和数据库投影。数据库不可用时的容量、满载策略、重复语义和告警属于Collector部署设计；Formatter成功不表示记录已经持久化。

`0.3.0`删除了`Client`、`AsyncClient`、`ClientConfig`、`LogEvent`、`EventKind`、自定义`Level`、`StellarmeshHandler`、远程重试、全局Client、shutdown和audit门面。项目必须先完成Collector验证，再升级依赖和删除logging-service配置。强事务审计继续使用业务数据库或transactional outbox。
