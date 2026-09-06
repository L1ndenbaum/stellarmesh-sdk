# stellarmesh-logging

`stellarmesh-logging` 为 Python 3.11 及以上项目提供标准库 `logging` 的安全 `JSONFormatter` 与 `PrettyFormatter`。当前公共版本为 `0.5.0`；不包含远程 Client、HTTP token、后台线程、Kafka、spool、数据库表或日志级别策略，也没有运行时第三方依赖。

```sh
pip install stellarmesh-logging==0.5.0
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

每条记录包含 `time`、`level`、`msg` 和 `logger`。项目可以用 `static_fields` 声明稳定字段，并通过 `LogRecord.extra` 添加自己的顶层字段；基础保留字段不能被覆盖。Formatter 支持异常结构化、Unicode、内置容器和敏感字段规范化后的精确匹配；固定字段和嵌套容器共享属性预算。`token_count`、`session_id` 不因敏感词子串而被遮盖。复杂业务对象、自定义容器和 dataclass 需先显式转为字段。

本地开发可改用 `PrettyFormatter`，构造参数与 `JSONFormatter` 相同。两者共享字段脱敏、预算和异常处理，只改变展示：pretty 使用无颜色的时间、级别、Logger、消息和 `key=value` 字段，异常堆栈缩进分行；终端控制字符会转义。JSON 仍是单行格式，现有消费者无需修改。

Formatter 不拥有 `StreamHandler` 或文件描述符，也不读取 `LOG_LEVEL`、`LOG_FORMAT` 等环境变量。输出到 stdout、stderr、文件或其他目标，格式选择、日志等级和 Handler 生命周期都由项目使用 Python 标准库配置。需要采集时使用 JSON，再由项目自己的 Vector 等 Collector 完成有界持久缓冲、恢复重放和数据库投影；pretty 不作为 JSON Collector 的输入。

`0.2.0` 是旧远程 Logging v2 客户端的最后版本。`0.3.0` 是有意的破坏性版本，删除了 `Client`、`LogEvent`、`StellarmeshHandler`、结构化审计门面和关闭入口；仍依赖旧 API 的项目必须先完成 Collector迁移，不能直接升级。事务性审计应写入业务数据库或 transactional outbox，不能把普通 stdout日志当作合规级不可丢失记录。

`0.4.0` 的支持类型、节点预算和故障责任有意收窄，升级前阅读[Python 接入与迁移教程](https://github.com/L1ndenbaum/stellarmesh-sdk/blob/dev/docs/sdk/python/README.md)和[字段清洗约定](https://github.com/L1ndenbaum/stellarmesh-sdk/blob/dev/contracts/logging/sanitization.md)。项目自己的凭据名称可通过 `extra_sensitive_keys` 扩展。
