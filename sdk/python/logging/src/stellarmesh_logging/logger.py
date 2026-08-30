"""由显式配置客户端支持的 Logger 门面。"""

from __future__ import annotations

import asyncio
import atexit
import sys
import threading
import traceback
from datetime import UTC, datetime
from typing import Any

from .client import Client
from .contracts import EventKind, Level


class Logger:
    """模块级结构化日志记录器。"""

    def __init__(
        self,
        name: str,
        *,
        client: Client,
        service: str | None = None,
        bound_metadata: dict[str, Any] | None = None,
    ) -> None:
        self.name = name
        self._client = client
        self._service = service or client.config.service
        self._bound_metadata = bound_metadata or {}

    def bind(self, **metadata: Any) -> Logger:
        """返回一个为每个事件附加元数据的日志记录器。"""
        return Logger(
            self.name,
            client=self._client,
            service=self._service,
            bound_metadata={**self._bound_metadata, **metadata},
        )

    def debug(
        self, message: str, *, trace_id: str | None = None, **metadata: Any
    ) -> bool:
        return self._emit(Level.DEBUG, message, trace_id, metadata)

    def info(
        self, message: str, *, trace_id: str | None = None, **metadata: Any
    ) -> bool:
        return self._emit(Level.INFO, message, trace_id, metadata)

    def warning(
        self, message: str, *, trace_id: str | None = None, **metadata: Any
    ) -> bool:
        return self._emit(Level.WARNING, message, trace_id, metadata)

    def error(
        self, message: str, *, trace_id: str | None = None, **metadata: Any
    ) -> bool:
        return self._emit(Level.ERROR, message, trace_id, metadata)

    def audit(
        self,
        message: str,
        *,
        level: Level = Level.INFO,
        trace_id: str | None = None,
        **metadata: Any,
    ) -> bool:
        return self._emit(
            level,
            message,
            trace_id,
            metadata,
            kind=EventKind.AUDIT,
        )

    def exception(
        self, message: str, *, trace_id: str | None = None, **metadata: Any
    ) -> bool:
        exc_type, exc_value, exc_traceback = sys.exc_info()
        if exc_type is not None:
            metadata.setdefault("exception_type", exc_type.__name__)
            metadata.setdefault("exception_message", str(exc_value))
            metadata.setdefault(
                "traceback",
                "".join(traceback.format_exception(exc_type, exc_value, exc_traceback)),
            )
        return self._emit(Level.ERROR, message, trace_id, metadata)

    def _emit(
        self,
        level: Level,
        message: str,
        trace_id: str | None,
        metadata: dict[str, Any],
        *,
        kind: EventKind = EventKind.LOG,
    ) -> bool:
        return self._client.emit_event(
            level,
            kind=kind,
            message=message,
            trace_id=trace_id,
            service=self._service,
            timestamp=datetime.now(UTC),
            metadata={"logger": self.name, **self._bound_metadata, **metadata},
        )


_default_client: Client | None = None
_default_client_lock = threading.Lock()


def set_default_client(client: Client | None) -> None:
    """替换进程级客户端，同时保持配置显式可见。"""
    global _default_client
    with _default_client_lock:
        previous = _default_client
        _default_client = client
    if previous is not None and previous is not client:
        previous.close()


def get_logger(
    name: str, *, client: Client | None = None, service: str | None = None
) -> Logger:
    """使用显式客户端或已配置的默认客户端创建日志记录器。"""
    resolved = client
    if resolved is None:
        with _default_client_lock:
            resolved = _default_client
    if resolved is None:
        raise RuntimeError("no default Stellarmesh logging client is configured")
    return Logger(name, client=resolved, service=service)


async def shutdown_logging(*, timeout: float = 2.0) -> bool:
    """解除默认客户端并异步排空队列。"""
    global _default_client
    with _default_client_lock:
        client = _default_client
        _default_client = None
    if client is None:
        return True
    return await client.aclose(timeout=timeout)


def shutdown_logging_sync(*, timeout: float = 2.0) -> bool:
    """为非异步入口同步排空默认客户端。"""
    try:
        asyncio.get_running_loop()
    except RuntimeError:
        return asyncio.run(shutdown_logging(timeout=timeout))
    raise RuntimeError("use await shutdown_logging() from an active event loop")


def _shutdown_at_exit() -> None:
    global _default_client
    with _default_client_lock:
        client = _default_client
        _default_client = None
    if client is not None:
        client.close(timeout=1.0)


atexit.register(_shutdown_at_exit)
