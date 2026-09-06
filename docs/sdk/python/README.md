# Python Logging SDK 接入教程

本教程对应已发布的 `stellarmesh-logging==0.5.0`。它要求 Python 3.11 及以上，没有运行时第三方依赖。它提供安全的 `JSONFormatter` 与 `PrettyFormatter`，不拥有 Handler、stream、环境变量、后台线程、远程服务或数据库。

## 安装

从正式 PyPI 安装固定版本：

```sh
python -m pip install stellarmesh-logging==0.5.0
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

## 本地可读输出

需要人直接阅读日志时，由应用选择 `PrettyFormatter`，无需更改 Logger 调用：

```python
from stellarmesh_logging import PrettyFormatter

handler.setFormatter(PrettyFormatter(static_fields={"service": "orders-api"}))
```

输出包含 UTC 时间、级别、Logger、消息与附加 `key=value` 字段；嵌套字段采用紧凑表示，异常堆栈缩进分行。默认不加入 ANSI 颜色，消息、字段和堆栈中的终端控制字符会转义。应用可定义 `LOG_FORMAT=pretty|json`，本地默认 pretty、生产显式 JSON；SDK 不读取该配置，也不自动探测 TTY 或 Collector。

两个 Formatter 接受相同的构造参数，共享同一字段清洗与预算实现。pretty 不降低脱敏保护，也不修改调用方记录；JSON 原有字段及单行契约保持不变。若改动环境配置，应重启进程或重新创建容器使之生效。不要同时向 stdout 和 stderr 输出同一事件的两种格式。

## 脱敏和限制

完整规则以[跨语言清洗约定](../../../contracts/logging/sanitization.md)为准。默认消息和字符串各 `16KiB`、项目节点 `64`、深度 `8`；固定字段、当前字段和所有嵌套容器共用预算。敏感字段规范化后精确匹配，`token_count`、`session_id` 不再因子串匹配而被遮盖。`extra_sensitive_keys` 可添加项目自己的准确凭据名称。

支持内置标量、dict/list/tuple、日期时间、UUID、Enum、异常和 bytes；dataclass、自定义 Mapping/Sequence 与容器子类不再自动展开。敏感值输出 `[REDACTED]`，不支持的值输出 `[UNSERIALIZABLE]`，超深或超长值使用 `[TRUNCATED]`。Formatter 不扫描消息或异常文本中的凭据。

日志等级由标准库Logger和Handler控制。Formatter不实现第二套`minimum_level`，也不会关闭调用方的stream。多进程或fork应用应在自己的进程初始化边界安装Handler，避免重复注册。

## 从 `0.3.0` 迁移

将 dataclass 或自定义容器显式转换为少量内置字典字段；检查此前依赖敏感词子串匹配的凭据名称，并添加到 `extra_sensitive_keys`。固定字段只复制顶层结构，项目不能在输出时并发修改嵌套容器。支持类型的消息、异常编码失败仍安全降级，输出 stream 和项目扩展代码的错误由项目负责。

## Collector与迁移

推荐把单行JSON写stdout，再由项目自己的Vector等Collector完成解析、有界磁盘buffer、恢复重放和数据库投影。数据库不可用时的容量、满载策略、重复语义和告警属于Collector部署设计；Formatter成功不表示记录已经持久化。

`0.3.0`删除了`Client`、`AsyncClient`、`ClientConfig`、`LogEvent`、`EventKind`、自定义`Level`、`StellarmeshHandler`、远程重试、全局Client、shutdown和audit门面。项目必须先完成Collector验证，再升级依赖和删除logging-service配置。强事务审计继续使用业务数据库或transactional outbox。
