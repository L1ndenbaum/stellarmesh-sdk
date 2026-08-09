"""Non-blocking batch client for the Stellarmesh logging ingester."""

from __future__ import annotations

import asyncio
import queue
import sys
import threading
import time
from collections.abc import Callable
from contextlib import suppress
from dataclasses import dataclass
from datetime import UTC, datetime
from enum import StrEnum
from typing import Any, TypeAlias, cast

import httpx

from .contracts import Level, LogEvent, normalize_level, should_emit_level
from .transport import BatchTransport

TraceIDProvider: TypeAlias = Callable[[], str]
DropHandler: TypeAlias = Callable[[LogEvent | None, Exception], None]
_STOP_WORKER = object()


class _ClientState(StrEnum):
    NEW = "new"
    RUNNING = "running"
    CLOSING = "closing"
    CLOSED = "closed"
    FAILED = "failed"


@dataclass(frozen=True, slots=True)
class ClientConfig:
    """Configuration for an asynchronous logging client."""

    base_url: str
    token: str
    service: str
    enabled: bool = True
    minimum_level: Level | str = Level.INFO
    timeout_seconds: float = 2.0
    queue_size: int = 4096
    batch_size: int = 128
    flush_interval_ms: int = 100
    max_body_bytes: int = 900 * 1024
    trace_id_provider: TraceIDProvider | None = None
    drop_handler: DropHandler | None = None


class Client:
    """Queue events locally and deliver bounded HTTP batches from a worker thread."""

    def __init__(
        self,
        config: ClientConfig,
        *,
        transport: httpx.BaseTransport | None = None,
    ) -> None:
        _validate_config(config)
        self.config = config
        self._minimum_level = normalize_level(config.minimum_level)
        self._batch_size = config.batch_size
        self._flush_interval = config.flush_interval_ms / 1000
        self._max_body_bytes = config.max_body_bytes
        self._transport = BatchTransport(
            base_url=config.base_url,
            token=config.token,
            timeout_seconds=config.timeout_seconds,
            transport=transport,
        )
        self._queue: queue.Queue[LogEvent | object] = queue.Queue(
            maxsize=config.queue_size
        )
        self._state_lock = threading.Lock()
        self._fallback_lock = threading.Lock()
        self._stop_requested = threading.Event()
        self._worker_done = threading.Event()
        self._worker: threading.Thread | None = None
        self._state = _ClientState.NEW
        self._failure: Exception | None = None
        self._last_fallback_warning = 0.0

    def emit_event(
        self,
        level: Level | str,
        *,
        message: str,
        trace_id: str | None = None,
        metadata: dict[str, Any] | None = None,
        timestamp: datetime | None = None,
        service: str | None = None,
    ) -> bool:
        """Build and queue one event without waiting for remote delivery."""
        try:
            normalized_level = normalize_level(level)
            resolved_trace_id = trace_id
            if resolved_trace_id is None:
                provider = self.config.trace_id_provider
                resolved_trace_id = provider() if provider is not None else ""
            event = LogEvent(
                timestamp=timestamp or datetime.now(UTC),
                level=normalized_level,
                service=service or self.config.service,
                message=message,
                trace_id=resolved_trace_id,
                metadata=metadata or {},
            )
        except Exception as exc:  # noqa: BLE001 - providers are isolated.
            self._drop(None, exc)
            return False
        return self.enqueue(event)

    def enqueue(self, event: LogEvent) -> bool:
        """Queue an already validated event."""
        if not self.config.enabled or not should_emit_level(
            event.level, self._minimum_level
        ):
            return False
        failure: Exception | None = None
        with self._state_lock:
            if self._state in {
                _ClientState.CLOSING,
                _ClientState.CLOSED,
                _ClientState.FAILED,
            }:
                failure = self._failure or RuntimeError(
                    f"logging client is {self._state.value}"
                )
            else:
                try:
                    self._queue.put_nowait(event)
                except queue.Full:
                    failure = RuntimeError("logging client queue is full")
                else:
                    self._ensure_worker_locked()
        if failure is not None:
            self._drop(event, failure)
            return False
        return True

    def close(self, *, timeout: float = 2.0) -> bool:
        """Stop accepting events and wait for queued delivery to finish."""
        worker = self._request_close()
        if worker is None or worker is threading.current_thread():
            return self._state_snapshot() is not _ClientState.FAILED
        worker.join(timeout=max(timeout, 0.0))
        if worker.is_alive():
            self._fallback_warning(
                f"logging client drain timed out; remaining={self._queue.qsize()}"
            )
            return False
        return self._state_snapshot() is _ClientState.CLOSED

    async def aclose(self, *, timeout: float = 2.0) -> bool:
        """Drain without blocking an asyncio event loop."""
        worker = self._request_close()
        if worker is None or worker is threading.current_thread():
            return self._state_snapshot() is not _ClientState.FAILED
        loop = asyncio.get_running_loop()
        deadline = loop.time() + max(timeout, 0.0)
        while worker.is_alive():
            remaining = deadline - loop.time()
            if remaining <= 0:
                self._fallback_warning(
                    f"logging client drain timed out; remaining={self._queue.qsize()}"
                )
                return False
            await asyncio.sleep(min(0.05, remaining))
        return self._state_snapshot() is _ClientState.CLOSED

    def _ensure_worker_locked(self) -> None:
        if self._worker is not None:
            return
        self._state = _ClientState.RUNNING
        self._worker = threading.Thread(
            target=self._worker_loop,
            name="stellarmesh-logging-client",
            daemon=True,
        )
        self._worker.start()

    def _request_close(self) -> threading.Thread | None:
        with self._state_lock:
            if self._state is _ClientState.NEW:
                self._state = _ClientState.CLOSED
                self._transport.close()
                self._worker_done.set()
                return None
            if self._state is _ClientState.RUNNING:
                self._state = _ClientState.CLOSING
                self._stop_requested.set()
                with suppress(queue.Full):
                    self._queue.put_nowait(_STOP_WORKER)
            return self._worker

    def _worker_loop(self) -> None:
        failure: Exception | None = None
        try:
            self._run_worker()
        except Exception as exc:  # noqa: BLE001 - isolate logging worker failures.
            failure = exc
            self._fallback_warning(f"logging worker failed: {exc}")
        finally:
            self._transport.close()
            if failure is not None:
                self._drain_failed_queue(failure)
            with self._state_lock:
                if failure is None:
                    self._state = _ClientState.CLOSED
                else:
                    self._state = _ClientState.FAILED
                    self._failure = failure
            self._worker_done.set()

    def _run_worker(self) -> None:
        while True:
            if self._stop_requested.is_set() and self._queue.empty():
                return
            try:
                first = self._queue.get(timeout=0.1)
            except queue.Empty:
                continue
            if first is _STOP_WORKER:
                self._queue.task_done()
                continue

            batch = [cast(LogEvent, first)]
            deadline = time.monotonic() + self._flush_interval
            while len(batch) < self._batch_size:
                try:
                    if self._stop_requested.is_set():
                        queued = self._queue.get_nowait()
                    else:
                        remaining = deadline - time.monotonic()
                        if remaining <= 0:
                            break
                        queued = self._queue.get(timeout=remaining)
                except queue.Empty:
                    break
                if queued is _STOP_WORKER:
                    self._queue.task_done()
                    break
                batch.append(cast(LogEvent, queued))

            try:
                self._send_batch(batch)
            finally:
                for _ in batch:
                    self._queue.task_done()

    def _send_batch(self, events: list[LogEvent]) -> bool:
        try:
            payload = self._transport.encode(events)
        except Exception as exc:  # noqa: BLE001 - isolate serialization failures.
            for event in events:
                self._drop(event, exc)
            return False
        if len(payload) > self._max_body_bytes:
            if len(events) == 1:
                self._drop(
                    events[0],
                    ValueError("logging event exceeds the client body limit"),
                )
                return False
            midpoint = len(events) // 2
            left_sent = self._send_batch(events[:midpoint])
            right_sent = self._send_batch(events[midpoint:])
            return left_sent and right_sent

        try:
            self._transport.send(events, payload)
        except Exception as exc:  # noqa: BLE001 - logging must not break callers.
            for event in events:
                self._drop(event, exc)
            return False
        return True

    def _drain_failed_queue(self, failure: Exception) -> None:
        while True:
            try:
                queued = self._queue.get_nowait()
            except queue.Empty:
                return
            try:
                if queued is not _STOP_WORKER:
                    self._drop(cast(LogEvent, queued), failure)
            finally:
                self._queue.task_done()

    def _drop(self, event: LogEvent | None, exc: Exception) -> None:
        handler = self.config.drop_handler
        if handler is None:
            self._fallback_warning(str(exc))
            return
        try:
            handler(event, exc)
        except Exception as callback_error:  # noqa: BLE001 - callbacks are isolated.
            self._fallback_warning(f"logging drop handler failed: {callback_error}")

    def _fallback_warning(self, message: str) -> None:
        with self._fallback_lock:
            now = time.monotonic()
            if (
                self._last_fallback_warning > 0
                and now - self._last_fallback_warning < 30
            ):
                return
            self._last_fallback_warning = now
            print(f"[stellarmesh-logging-fallback] {message}", file=sys.stderr)

    def _state_snapshot(self) -> _ClientState:
        with self._state_lock:
            return self._state


def _validate_config(config: ClientConfig) -> None:
    try:
        url = httpx.URL(config.base_url)
    except Exception as exc:  # noqa: BLE001 - normalize configuration errors.
        raise ValueError("logging base URL is invalid") from exc
    if url.scheme not in {"http", "https"} or not url.host:
        raise ValueError("logging base URL must be an absolute HTTP or HTTPS URL")
    if not config.token.strip():
        raise ValueError("logging service token is required")
    if not config.service.strip():
        raise ValueError("logging service name is required")
    if config.timeout_seconds <= 0:
        raise ValueError("logging timeout_seconds must be positive")
    if config.queue_size <= 0:
        raise ValueError("logging queue_size must be positive")
    if config.batch_size <= 0:
        raise ValueError("logging batch_size must be positive")
    if config.flush_interval_ms <= 0:
        raise ValueError("logging flush_interval_ms must be positive")
    if config.max_body_bytes <= 0:
        raise ValueError("logging max_body_bytes must be positive")
    normalize_level(config.minimum_level)
