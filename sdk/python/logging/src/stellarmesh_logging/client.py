"""面向 Stellarmesh 日志接收服务的非阻塞批量客户端。"""

from __future__ import annotations

import asyncio
import queue
import random
import sys
import threading
import time
from collections.abc import Callable
from contextlib import suppress
from dataclasses import dataclass
from datetime import UTC, datetime
from email.utils import parsedate_to_datetime
from enum import StrEnum
from typing import Any, TypeAlias, cast

import httpx

from .codec import encode_event
from .contracts import (
    MAX_EVENT_JSON_BYTES,
    MAX_HTTP_BODY_BYTES,
    EventKind,
    Level,
    LogEvent,
    normalize_level,
    should_emit_level,
)
from .transport import BatchTransport

TraceIDProvider: TypeAlias = Callable[[], str]
DropHandler: TypeAlias = Callable[[LogEvent | None, Exception], None]
_STOP_WORKER = object()
_MAX_QUEUE_EVENTS = 1_000_000
_MAX_QUEUE_BYTES = 1 << 30
_MAX_BATCH_EVENTS = 10_000
_MAX_ATTEMPTS = 10
_MAX_DURATION_SECONDS = 3600.0


class _ClientState(StrEnum):
    NEW = "new"
    RUNNING = "running"
    CLOSING = "closing"
    CLOSED = "closed"
    FAILED = "failed"


@dataclass(frozen=True, slots=True)
class ClientConfig:
    """异步日志客户端配置。"""

    base_url: str
    token: str
    service: str
    enabled: bool = True
    minimum_level: Level | str = Level.INFO
    timeout_seconds: float = 7.0
    queue_size: int = 4096
    queue_bytes: int = 16 << 20
    batch_size: int = 128
    flush_interval_ms: int = 100
    max_body_bytes: int = MAX_HTTP_BODY_BYTES
    max_attempts: int = 3
    initial_backoff_seconds: float = 0.1
    max_backoff_seconds: float = 1.0
    max_retry_after_seconds: float = 30.0
    trace_id_provider: TraceIDProvider | None = None
    drop_handler: DropHandler | None = None


@dataclass(frozen=True, slots=True)
class _QueuedEvent:
    event: LogEvent
    bytes: int


class Client:
    """在本地排队事件，并由工作线程通过有界 HTTP 批次发送。"""

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
        self._max_attempts = config.max_attempts
        self._initial_backoff = config.initial_backoff_seconds
        self._max_backoff = config.max_backoff_seconds
        self._max_retry_after = config.max_retry_after_seconds
        self._transport = BatchTransport(
            base_url=config.base_url,
            token=config.token,
            timeout_seconds=config.timeout_seconds,
            transport=transport,
        )
        self._queue: queue.Queue[_QueuedEvent | object] = queue.Queue(
            maxsize=config.queue_size
        )
        self._state_lock = threading.Lock()
        self._fallback_lock = threading.Lock()
        self._stop_requested = threading.Event()
        self._abort_requested = threading.Event()
        self._worker_done = threading.Event()
        self._worker: threading.Thread | None = None
        self._state = _ClientState.NEW
        self._failure: Exception | None = None
        self._last_fallback_warning = 0.0
        self._queued_events = 0
        self._queued_bytes = 0

    def emit_event(
        self,
        level: Level | str,
        *,
        message: str,
        trace_id: str | None = None,
        metadata: dict[str, Any] | None = None,
        timestamp: datetime | None = None,
        service: str | None = None,
        kind: EventKind = EventKind.LOG,
    ) -> bool:
        """构造并入队一个事件，不等待远端投递。"""
        try:
            normalized_level = normalize_level(level)
            resolved_trace_id = trace_id
            if resolved_trace_id is None:
                provider = self.config.trace_id_provider
                resolved_trace_id = provider() if provider is not None else ""
            event = LogEvent(
                timestamp=timestamp or datetime.now(UTC),
                kind=kind,
                level=normalized_level,
                service=service or self.config.service,
                message=message,
                trace_id=resolved_trace_id,
                metadata=metadata or {},
            )
        except Exception as exc:  # noqa: BLE001 - provider 异常需要隔离。
            self._drop(None, exc)
            return False
        return self.enqueue(event)

    def enqueue(self, event: LogEvent) -> bool:
        """将已校验的事件加入队列。"""
        if not self.config.enabled:
            return False
        if event.kind is EventKind.LOG and not should_emit_level(
            event.level, self._minimum_level
        ):
            return False
        try:
            snapshot = event.model_copy(deep=True)
            event_bytes = len(encode_event(snapshot))
        except Exception as exc:  # noqa: BLE001 - 序列化异常需要隔离。
            self._drop(event, exc)
            return False
        if event_bytes > MAX_EVENT_JSON_BYTES:
            self._drop(event, ValueError("logging event exceeds the contract limit"))
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
            elif (
                self._queued_events >= self.config.queue_size
                or event_bytes > self.config.queue_bytes - self._queued_bytes
            ):
                failure = RuntimeError("logging client queue is full")
            else:
                try:
                    self._queue.put_nowait(_QueuedEvent(snapshot, event_bytes))
                except queue.Full:
                    failure = RuntimeError("logging client queue is full")
                else:
                    self._queued_events += 1
                    self._queued_bytes += event_bytes
                    self._ensure_worker_locked()
        if failure is not None:
            self._drop(event, failure)
            return False
        return True

    def close(self, *, timeout: float = 2.0) -> bool:
        """停止接收事件，并等待队列中的投递完成。"""
        worker = self._request_close()
        if worker is None or worker is threading.current_thread():
            return self._state_snapshot() is not _ClientState.FAILED
        worker.join(timeout=max(timeout, 0.0))
        if worker.is_alive():
            self._abort_requested.set()
            self._fallback_warning(
                f"logging client drain timed out; remaining={self._pending_count()}"
            )
            return False
        return self._state_snapshot() is _ClientState.CLOSED

    async def aclose(self, *, timeout: float = 2.0) -> bool:
        """在不阻塞 asyncio 事件循环的情况下排空队列。"""
        worker = self._request_close()
        if worker is None or worker is threading.current_thread():
            return self._state_snapshot() is not _ClientState.FAILED
        loop = asyncio.get_running_loop()
        deadline = loop.time() + max(timeout, 0.0)
        while worker.is_alive():
            remaining = deadline - loop.time()
            if remaining <= 0:
                self._abort_requested.set()
                self._fallback_warning(
                    f"logging client drain timed out; remaining={self._pending_count()}"
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
        except Exception as exc:  # noqa: BLE001 - 隔离日志工作线程故障。
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

            batch = [cast(_QueuedEvent, first)]
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
                batch.append(cast(_QueuedEvent, queued))

            try:
                self._send_batch([item.event for item in batch])
            finally:
                for item in batch:
                    self._queue.task_done()
                    self._release(item)

    def _send_batch(self, events: list[LogEvent]) -> bool:
        try:
            payload = self._transport.encode(events)
        except Exception as exc:  # noqa: BLE001 - 隔离序列化故障。
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

        last_error: Exception | None = None
        for attempt in range(1, self._max_attempts + 1):
            try:
                self._transport.send(events, payload)
            except Exception as exc:  # noqa: BLE001 - 日志不能中断调用方。
                last_error = exc
                if not _retryable_error(exc) or attempt == self._max_attempts:
                    break
                retry_after = _retry_after_delay(exc, self._max_retry_after)
                if self._abort_requested.wait(
                    max(self._retry_delay(attempt), retry_after)
                ):
                    break
            else:
                return True
        assert last_error is not None
        for event in events:
            self._drop(event, last_error)
        return False

    def _drain_failed_queue(self, failure: Exception) -> None:
        while True:
            try:
                queued = self._queue.get_nowait()
            except queue.Empty:
                return
            try:
                if queued is not _STOP_WORKER:
                    item = cast(_QueuedEvent, queued)
                    self._drop(item.event, failure)
                    self._release(item)
            finally:
                self._queue.task_done()

    def _drop(self, event: LogEvent | None, exc: Exception) -> None:
        handler = self.config.drop_handler
        if handler is None:
            self._fallback_warning(str(exc))
            return
        try:
            handler(event, exc)
        except Exception as callback_error:  # noqa: BLE001 - callback 异常需要隔离。
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

    def _pending_count(self) -> int:
        with self._state_lock:
            return self._queued_events

    def _release(self, item: _QueuedEvent) -> None:
        with self._state_lock:
            self._queued_events -= 1
            self._queued_bytes -= item.bytes

    def _retry_delay(self, failed_attempt: int) -> float:
        delay = min(
            self._initial_backoff * (2 ** (failed_attempt - 1)),
            self._max_backoff,
        )
        return random.uniform(0.0, delay)


def _validate_config(config: ClientConfig) -> None:
    try:
        url = httpx.URL(config.base_url)
    except Exception as exc:  # noqa: BLE001 - 统一配置错误。
        raise ValueError("logging base URL is invalid") from exc
    if url.scheme not in {"http", "https"} or not url.host:
        raise ValueError("logging base URL must be an absolute HTTP or HTTPS URL")
    if not config.token.strip():
        raise ValueError("logging service token is required")
    if not config.service.strip() or config.service != config.service.strip():
        raise ValueError("logging service name must be non-empty and trimmed")
    if config.timeout_seconds <= 0:
        raise ValueError("logging timeout_seconds must be positive")
    if config.queue_size <= 0:
        raise ValueError("logging queue_size must be positive")
    if config.queue_bytes <= 0:
        raise ValueError("logging queue_bytes must be positive")
    if config.batch_size <= 0:
        raise ValueError("logging batch_size must be positive")
    if config.flush_interval_ms <= 0:
        raise ValueError("logging flush_interval_ms must be positive")
    if config.max_body_bytes <= 0:
        raise ValueError("logging max_body_bytes must be positive")
    if config.max_body_bytes > MAX_HTTP_BODY_BYTES:
        raise ValueError(
            f"logging max_body_bytes must not exceed {MAX_HTTP_BODY_BYTES}"
        )
    if config.max_attempts <= 0:
        raise ValueError("logging max_attempts must be positive")
    if config.initial_backoff_seconds <= 0:
        raise ValueError("logging initial_backoff_seconds must be positive")
    if config.max_backoff_seconds <= 0:
        raise ValueError("logging max_backoff_seconds must be positive")
    if config.max_retry_after_seconds <= 0:
        raise ValueError("logging max_retry_after_seconds must be positive")
    if config.initial_backoff_seconds > config.max_backoff_seconds:
        raise ValueError(
            "logging initial_backoff_seconds must not exceed max_backoff_seconds"
        )
    if config.max_retry_after_seconds < config.max_backoff_seconds:
        raise ValueError(
            "logging max_retry_after_seconds must not be less than max_backoff_seconds"
        )
    if config.queue_size > _MAX_QUEUE_EVENTS:
        raise ValueError("logging queue_size is outside supported bounds")
    if config.queue_bytes > _MAX_QUEUE_BYTES:
        raise ValueError("logging queue_bytes is outside supported bounds")
    if config.batch_size > _MAX_BATCH_EVENTS:
        raise ValueError("logging batch_size is outside supported bounds")
    if config.max_attempts > _MAX_ATTEMPTS:
        raise ValueError("logging max_attempts is outside supported bounds")
    if any(
        duration > _MAX_DURATION_SECONDS
        for duration in (
            config.timeout_seconds,
            config.flush_interval_ms / 1000,
            config.initial_backoff_seconds,
            config.max_backoff_seconds,
            config.max_retry_after_seconds,
        )
    ):
        raise ValueError("logging client duration is outside supported bounds")
    normalize_level(config.minimum_level)


def _retryable_error(exc: Exception) -> bool:
    if isinstance(exc, httpx.TransportError):
        return True
    if isinstance(exc, httpx.HTTPStatusError):
        return exc.response.status_code in {408, 425, 429, 500, 502, 503, 504}
    return False


def _retry_after_delay(exc: Exception, maximum: float) -> float:
    if not isinstance(exc, httpx.HTTPStatusError):
        return 0.0
    value = exc.response.headers.get("Retry-After", "").strip()
    if not value:
        return 0.0
    try:
        seconds = int(value)
    except ValueError:
        try:
            when = parsedate_to_datetime(value)
        except (TypeError, ValueError, OverflowError):
            return 0.0
        if when.tzinfo is None:
            return 0.0
        seconds_value = (when - datetime.now(UTC)).total_seconds()
    else:
        seconds_value = float(seconds)
    if seconds_value <= 0:
        return 0.0
    return min(seconds_value, maximum)
