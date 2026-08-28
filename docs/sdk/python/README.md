# Python SDK 接入教程

本教程只对应独立 distribution `stellarmesh-logging`。需要对象存储控制面、预签名直传、同步或异步文件客户端时，阅读[Python 对象存储 SDK 接入教程](storage.md)并安装独立的 `stellarmesh-storage`，不要假定安装日志包会附带对象存储能力。

本教程对应 Python distribution `stellarmesh-logging`，import 名称为 `stellarmesh_logging`，要求 Python 3.11 及以上版本。

## 1. 安装固定版本

包发布到 PyPI 后，业务项目应固定版本：

```sh
python -m pip install stellarmesh-logging==0.1.2
```

使用 `requirements.txt`、锁文件或项目依赖管理器时，也应保留精确版本约束。生产镜像不要直接安装可变 Git 分支。

仅在本地联调 SDK 源码时，可以从相邻仓库安装：

```sh
python -m pip install -e ../stellarmesh-sdk/sdk/python
```

## 2. 准备业务配置

SDK 不读取业务项目的 settings 或 `.env`。业务配置层需要向初始化代码提供：

- `base_url`：`logging-service` HTTP 根地址，不包含 API path；
- `token`：通过 Secret 注入的服务 token；
- `service`：稳定的业务服务名，必须与 token 授权一致、非空且没有首尾空白；
- 可选的最低日志级别、队列和批量限制。

## 3. 初始化客户端

推荐在应用生命周期入口创建一个进程级 `Client`，并显式设置默认客户端：

```python
from __future__ import annotations

import contextvars
import logging
import sys

from stellarmesh_logging import (
    Client,
    ClientConfig,
    Level,
    StellarmeshHandler,
    set_default_client,
)

trace_id_var: contextvars.ContextVar[str] = contextvars.ContextVar(
    "trace_id",
    default="",
)


def report_drop(event: object, error: Exception) -> None:
    print(
        f"remote log dropped: event={event!r} error={error}",
        file=sys.stderr,
    )


client = Client(
    ClientConfig(
        base_url="http://logging-service:8091",
        token="由业务配置层注入",
        service="example-worker",
        minimum_level=Level.INFO,
        trace_id_provider=trace_id_var.get,
        drop_handler=report_drop,
    )
)
set_default_client(client)

root_logger = logging.getLogger()
root_logger.setLevel(logging.INFO)
root_logger.addHandler(StellarmeshHandler(client))

logger = logging.getLogger(__name__)
```

一个进程通常只需要一个默认客户端。`set_default_client` 会关闭此前的不同客户端，因此不要在每个请求中反复调用。SDK 不直接依赖 FastAPI、Django、Flask、Celery、OpenTelemetry 或业务 settings；框架适配应留在业务项目的应用入口。

`drop_handler` 可能由后台发送线程调用，必须线程安全、快速返回，并且不能再次调用当前远端 logger。callback 自身失败时，SDK 会限频写入标准错误输出。

`StellarmeshHandler` 只负责把标准库产生的 `LogRecord` 转换成规范事件并交给现有客户端。它不会创建控制台输出、额外队列或重试线程，也不会在关闭时替业务项目排空客户端。业务项目如果需要控制台日志，应自行配置 `StreamHandler`；不要重复注册远程 Handler，否则同一记录会发送多次。

logger 的有效级别决定一条记录是否会到达 Handler，`ClientConfig.minimum_level` 再决定它是否进入远程队列。建议由业务项目按现有 logging 配置管理前者，并把后者作为远程持久化的统一最低级别。

## 4. 使用标准 logging

```python
logger.info(
    "job %s started",
    "job-123",
    extra={"job_id": "job-123", "queue": "default"},
)
logger.warning(
    "job delayed",
    extra={"job_id": "job-123", "delay_seconds": 12},
)

try:
    raise RuntimeError("example failure")
except RuntimeError:
    logger.exception("job failed", extra={"job_id": "job-123"})
```

Handler 支持标准库的 `%s` 延迟格式化、`exc_info`、`stack_info` 和 `extra`。它会记录 logger 名称、模块、函数与行号，把 `CRITICAL` 映射为契约中的 `ERROR`，并把名称为 `AUDIT` 的自定义级别映射为 `AUDIT`。标准 logging 方法仍返回 `None`；级别过滤、队列已满、客户端已关闭或后台发送失败都不会从业务日志调用中抛出。

metadata 会进行深度、数量、字符串长度和敏感字段处理。敏感 key 会先转小写并去除非字母数字字符，因此 `apiKey`、`api_key`、`api-key` 和大小写变体都会命中；异常对象会转成受限字符串。业务代码仍不应把 token、密码、Cookie、Authorization 或其他 Secret 主动放入日志。

## 5. 传播 trace ID

`trace_id_provider` 是无参数 callable。同步项目可以从线程本地状态读取；asyncio 项目建议使用 `contextvars.ContextVar`。通过 `extra` 传入 `trace_id` 时优先使用显式值：

```python
logger.info(
    "request completed",
    extra={"trace_id": "trace-from-request", "status_code": 200},
)
```

业务 middleware 应在请求开始时设置 ContextVar，并在请求结束时 reset，避免不同请求复用错误的 trace ID。

## 6. 在应用关闭时排空

异步应用应在 lifespan 或 shutdown hook 中等待：

```python
from stellarmesh_logging import shutdown_logging


async def application_shutdown() -> None:
    drained = await shutdown_logging(timeout=10.0)
    if not drained:
        # 记录本地指标或标准错误，不能再次调用已经关闭的远端 logger。
        pass
```

同步入口使用：

```python
from stellarmesh_logging import shutdown_logging_sync

drained = shutdown_logging_sync(timeout=10.0)
```

存在正在运行的 asyncio event loop 时，必须 `await shutdown_logging()`，不能调用同步包装器。也可以在没有设置默认客户端时直接保存 `Client`，分别调用 `client.close(timeout=...)` 或 `await client.aclose(timeout=...)`。

建议顺序是：停止接收请求、停止产生新日志的后台任务、排空日志客户端、退出进程。`atexit` 只提供一秒钟的最后保护，不应替代应用自己的关闭流程。

## 7. 常用客户端参数

| 字段 | 默认值 | 说明 |
| --- | --- | --- |
| `enabled` | `True` | 关闭后所有事件均不入队 |
| `minimum_level` | `INFO` | SDK 本地最低级别 |
| `timeout_seconds` | `7.0` | 单次 HTTP 请求超时 |
| `queue_size` | `4096` | 本地事件队列容量 |
| `queue_bytes` | `16MiB` | 尚未完成发送的规范化事件累计字节上限 |
| `batch_size` | `128` | 单次发送目标事件数 |
| `flush_interval_ms` | `100` | 未达到批量大小时的刷新周期 |
| `max_body_bytes` | `1MiB` | 单次 HTTP body 上限，包含 batch envelope；超限批次会继续拆分 |
| `max_attempts` | `3` | 单批总尝试次数，包含首次请求 |
| `initial_backoff_seconds` | `0.1` | 首次重试的最大抖动退避 |
| `max_backoff_seconds` | `1.0` | 指数退避上限 |
| `max_retry_after_seconds` | `30.0` | 服务端 `Retry-After` 等待上限，最大允许 `3600.0` |
| `trace_id_provider` | 空 | 从业务上下文读取 trace ID |
| `drop_handler` | 空 | 无法入队或发送时的 callback |

构造 `Client` 时会立即校验 URL、token、service、日志级别和所有正数限制。队列最多 `1000000` 条或 `1GiB`，批次最多 `10000` 条，尝试次数最多 `10`，时间参数最多一小时；配置错误应让应用启动失败，而不是延迟到后台线程。SDK 只重试网络异常和 `408`、`425`、`429`、`500`、`502`、`503`、`504`；其他 4xx 与格式异常的成功响应不会重试。合法的 `Retry-After` 可以是整数秒或 HTTP-date，实际等待取本地抖动退避与服务端要求的较大值，再受 `max_retry_after_seconds` 限制；非法、负数和已过期值会被忽略。`close()` 或 `aclose()` 超时会中断这段等待。请求结果不确定时可能已经被服务端接受，重试复用相同 `event_id`，下游仍须允许重复。

默认 `7s` 是按服务端默认 `500ms` 聚合等待和 `5s` Kafka 发布超时预留的客户端预算。如果部署提高 `STELLARMESH_LOGGING_BATCH_FLUSH_INTERVAL` 或 `STELLARMESH_LOGGING_KAFKA_PUBLISH_TIMEOUT`，必须同步提高 `timeout_seconds`，否则客户端可能在服务端完成持久确认前超时并重试。

## 8. 使用结构化日志门面

需要 `audit`、`bind` 或检查事件是否成功入队时，可以继续使用 SDK 自带的结构化日志门面：

```python
from stellarmesh_logging import get_logger

structured_logger = get_logger("example.module", client=client).bind(
    component="scheduler"
)
accepted = structured_logger.info("isolated event", test_case="example")
structured_logger.audit("job manually retried", operator_id="user-7")
```

日志门面提供 `debug`、`info`、`warning`、`error`、`audit` 和 `exception`，metadata 使用关键字参数传入，各方法返回是否成功进入本地队列。它不接收标准 logging 的 printf 风格位置参数。未设置默认客户端且没有显式传入 `client` 时，`get_logger` 会抛出 `RuntimeError`，防止配置缺失时静默丢日志。

## 9. 接入验证

在业务测试环境完成以下检查：

1. 初始化客户端和 Handler 后，通过标准 logger 发送一条带唯一测试标识的 `INFO` 日志；
2. 确认业务调用未受影响，`drop_handler` 未收到错误；
3. 执行应用 shutdown，确认排空结果为 `True`；
4. 确认 `logging-service` 的 `/health/ready` 为 `200`；
5. 在 ClickHouse 中按 `service` 和测试标识找到事件；
6. 模拟 `logging-service` 不可用，确认业务流程继续执行且 drop 指标能够告警；
7. async 项目额外确认关闭等待不会阻塞 event loop。

SDK 调用成功、本地队列排空、`logging-service` 持久确认和 ClickHouse 最终可查询是不同检查点，监控时不要合并为一个成功率。

## 10. 升级注意事项

- 固定 `stellarmesh-logging` 版本并提交锁文件；
- 升级后运行业务项目的 Ruff、mypy 和 pytest；
- 已有标准 logging 调用优先注册 `StellarmeshHandler`，不要为了接入而批量改写调用点；
- 只注册一次远程 Handler，并根据 logger 名称控制是否包含第三方库日志；
- `service` 改名必须同步更新 token 绑定、告警和查询口径；
- 调整队列和批量大小时，应同时观察 drop 计数、进程内存和关闭排空时间；
- Python SDK、Go SDK 与服务镜像应尽量来自同一发布 commit，避免契约漂移。
