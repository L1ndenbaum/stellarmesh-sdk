# stellarmesh-logging

> `0.2.0` 是远程 Logging v2 客户端的最后版本。公共日志运行时已经退役；后续版本改为标准库结构化 stdout 辅助能力。

`stellarmesh-logging` 为 Python 3.11 及以上项目提供 Logging v2 严格模型、标准库 `logging.Handler`、结构化日志门面和有界异步批量 HTTP 客户端。v2 使用 `kind=LOG|AUDIT` 表示事件种类，使用 `level=DEBUG|INFO|WARNING|ERROR` 表示严重程度。

```sh
python -m pip install stellarmesh-logging==0.2.0
```

```python
import logging

from stellarmesh_logging import Client, ClientConfig, StellarmeshHandler

client = Client(
    ClientConfig(
        base_url="http://logging-service:8091",
        token="由业务配置层注入",
        service="example-worker",
    )
)
logger = logging.getLogger("example")
logger.addHandler(StellarmeshHandler(client))
logger.info("job started", extra={"job_id": "job-123"})

client.close(timeout=10.0)
```

标准库 `StellarmeshHandler` 始终生成 `kind=LOG`。审计事件必须通过结构化门面显式产生：

```python
from stellarmesh_logging import Level, get_logger

audit_logger = get_logger("example.audit", client=client)
audit_logger.audit(
    "role granted",
    level=Level.INFO,
    action="iam.role.grant",
    outcome="success",
)
```

`AUDIT` 会在客户端整体启用时绕过 `minimum_level`，但仍可能因为队列已满、关闭超时或远程发送失败而丢弃。它不是合规级不可丢失或 WORM 审计存储。

客户端只在内存中排队日志，不提供本地持久 spool。日志调用成功只表示事件已经进入本地队列；收到 `logging-service` 的合法 `202` 后，才表示 Kafka 或服务端持久 spool 已经确认。网络结果不确定时可能产生重复事件，链路按 at-least-once 边界设计。

`service` 必须非空且没有首尾空白。token 只发送给 `logging-service`，metadata 会限制深度、数量和字符串长度，并对规范化后的敏感 key 脱敏。应用退出前应显式调用 `close()` 或 `aclose()`，并通过 `drop_handler` 观测队列满、校验失败、发送失败和关闭超时。

完整配置、标准 Handler、trace 传播、重试和关闭语义见项目中的 `docs/sdk/python/README.md`。
