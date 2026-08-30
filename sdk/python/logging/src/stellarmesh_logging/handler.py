"""面向 Stellarmesh 客户端的标准库 logging 适配器。"""

from __future__ import annotations

import logging
from datetime import UTC, datetime
from typing import Any

from .client import Client
from .contracts import Level

_STANDARD_RECORD_ATTRIBUTES = frozenset(
    logging.LogRecord(
        name="",
        level=logging.NOTSET,
        pathname="",
        lineno=0,
        msg="",
        args=(),
        exc_info=None,
    ).__dict__
) | {"asctime", "message"}
_HANDLER_METADATA_ATTRIBUTES = frozenset(
    {"logger", "module", "function", "line", "stack_info"}
)


class StellarmeshHandler(logging.Handler):
    """将标准 ``LogRecord`` 实例转换为 Stellarmesh 事件。"""

    def __init__(self, client: Client, *, service: str | None = None) -> None:
        super().__init__()
        self._client = client
        self._service = service

    def emit(self, record: logging.LogRecord) -> None:
        """转换并入队一条记录，不执行网络 I/O。"""
        try:
            metadata, trace_id = _record_metadata(record)
            self._client.emit_event(
                _record_level(record),
                message=record.getMessage(),
                trace_id=trace_id,
                service=self._service,
                timestamp=datetime.fromtimestamp(record.created, UTC),
                metadata=metadata,
            )
        except Exception:  # noqa: BLE001 - 日志不能中断调用方。
            try:
                self.handleError(record)
            except Exception:  # noqa: BLE001 - 错误报告不能递归。
                return


def _record_level(record: logging.LogRecord) -> Level:
    if record.levelno >= logging.ERROR:
        return Level.ERROR
    if record.levelno >= logging.WARNING:
        return Level.WARNING
    if record.levelno >= logging.INFO:
        return Level.INFO
    return Level.DEBUG


def _record_metadata(record: logging.LogRecord) -> tuple[dict[str, Any], str | None]:
    extra = {
        key: value
        for key, value in record.__dict__.items()
        if key not in _STANDARD_RECORD_ATTRIBUTES
        and key not in _HANDLER_METADATA_ATTRIBUTES
        and key != "trace_id"
    }
    metadata: dict[str, Any] = {
        **extra,
        "logger": record.name,
        "module": record.module,
        "function": record.funcName,
        "line": record.lineno,
    }
    if record.exc_info:
        exc_type, exc_value, _ = record.exc_info
        if exc_type is not None:
            metadata.update(
                {
                    "exception_type": exc_type.__name__,
                    "exception_message": str(exc_value),
                    "traceback": logging.Formatter().formatException(record.exc_info),
                }
            )
    elif record.exc_text:
        metadata["traceback"] = record.exc_text
    if record.stack_info:
        metadata["stack_info"] = record.stack_info

    raw_trace_id = record.__dict__.get("trace_id")
    trace_id = None if raw_trace_id is None else str(raw_trace_id)
    return metadata, trace_id


__all__ = ["StellarmeshHandler"]
