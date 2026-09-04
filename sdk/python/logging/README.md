# stellarmesh-logging

`stellarmesh-logging` 为 Python 3.11 及以上项目提供标准库 `logging` 的安全单行 JSON Formatter。`0.3.0` 不包含远程 Client、HTTP token、后台线程、Kafka、spool、数据库表或日志级别策略，也没有运行时第三方依赖。

```sh
pip install stellarmesh-logging==0.3.0
```

```python
import logging
import sys

from stellarmesh_logging import JSONFormatter

handler = logging.StreamHandler(sys.stdout)
handler.setFormatter(
    JSONFormatter(
        static_fields={
            "service": "orders-api",
            "environment": "production",
        }
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

每条记录包含 `time`、`level`、`msg` 和 `logger`。项目可以用 `static_fields` 声明稳定字段，并通过 `LogRecord.extra` 添加自己的顶层字段；基础保留字段不能被覆盖。Formatter 支持异常结构化、Unicode、嵌套值、敏感字段规范化脱敏，以及消息、字符串、属性数量和嵌套深度限制。

Formatter 不拥有 `StreamHandler` 或文件描述符。输出到 stdout、stderr、文件或其他目标，日志等级和 Handler 生命周期都由项目使用 Python 标准库配置。推荐输出结构化 stdout，再由项目自己的 Vector 等 Collector完成有界持久缓冲、恢复重放和数据库投影。

`0.2.0` 是旧远程 Logging v2 客户端的最后版本。`0.3.0` 是有意的破坏性版本，删除了 `Client`、`LogEvent`、`StellarmeshHandler`、结构化审计门面和关闭入口；仍依赖旧 API 的项目必须先完成 Collector迁移，不能直接升级。事务性审计应写入业务数据库或 transactional outbox，不能把普通 stdout日志当作合规级不可丢失记录。
