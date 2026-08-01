"""Logger facade backed by an explicitly configured client."""

from __future__ import annotations

import asyncio
import sys
import threading
import traceback
from datetime import UTC, datetime
from typing import Any

from .client import Client
from .contracts import Level


class Logger:
    """Module-scoped structured logger."""

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
        """Return a logger with metadata attached to every event."""
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
        self, message: str, *, trace_id: str | None = None, **metadata: Any
    ) -> bool:
        return self._emit(Level.AUDIT, message, trace_id, metadata)

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
    ) -> bool:
        return self._client.emit_event(
            level,
            message=message,
            trace_id=trace_id,
            service=self._service,
            timestamp=datetime.now(UTC),
            metadata={"logger": self.name, **self._bound_metadata, **metadata},
        )


_default_client: Client | None = None
_default_client_lock = threading.Lock()


def set_default_client(client: Client | None) -> None:
    """Replace the process-wide client without hiding its configuration."""
    global _default_client
    with _default_client_lock:
        previous = _default_client
        _default_client = client
    if previous is not None and previous is not client:
        previous.close()


def get_logger(
    name: str, *, client: Client | None = None, service: str | None = None
) -> Logger:
    """Create a logger from an explicit or previously configured default client."""
    resolved = client
    if resolved is None:
        with _default_client_lock:
            resolved = _default_client
    if resolved is None:
        raise RuntimeError("no default Stellarmesh logging client is configured")
    return Logger(name, client=resolved, service=service)


async def shutdown_logging(*, timeout: float = 2.0) -> bool:
    """Detach and asynchronously drain the default client."""
    global _default_client
    with _default_client_lock:
        client = _default_client
        _default_client = None
    if client is None:
        return True
    return await client.aclose(timeout=timeout)


def shutdown_logging_sync(*, timeout: float = 2.0) -> bool:
    """Synchronously drain the default client for non-async entrypoints."""
    try:
        asyncio.get_running_loop()
    except RuntimeError:
        return asyncio.run(shutdown_logging(timeout=timeout))
    raise RuntimeError("use await shutdown_logging() from an active event loop")
