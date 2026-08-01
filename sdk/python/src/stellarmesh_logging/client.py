"""Non-blocking batch client for the Stellarmesh logging ingester."""

from __future__ import annotations

import asyncio
import json
import queue
import sys
import threading
import time
from collections.abc import Callable
from contextlib import suppress
from dataclasses import dataclass
from datetime import UTC, datetime
from typing import Any, TypeAlias, cast

import httpx
from pydantic import ValidationError

from .contracts import (
    BatchIngestRequest,
    Level,
    LogEvent,
    normalize_level,
    should_emit_level,
)

TraceIDProvider: TypeAlias = Callable[[], str]
DropHandler: TypeAlias = Callable[[LogEvent | None, Exception], None]
_STOP_WORKER = object()


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
        self.config = config
        self._base_url = config.base_url.rstrip("/")
        self._minimum_level = normalize_level(config.minimum_level)
        self._timeout = max(config.timeout_seconds, 0.001)
        self._batch_size = max(config.batch_size, 1)
        self._flush_interval = max(config.flush_interval_ms, 1) / 1000
        self._max_body_bytes = max(config.max_body_bytes, 1)
        self._transport = transport
        self._queue: queue.Queue[LogEvent | object] = queue.Queue(
            maxsize=max(config.queue_size, 1)
        )
        self._state_lock = threading.Lock()
        self._stop_requested = threading.Event()
        self._worker_done = threading.Event()
        self._worker: threading.Thread | None = None
        self._closed = False
        self._http_client: httpx.Client | None = None
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
        except (TypeError, ValueError, ValidationError) as exc:
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
            if self._closed:
                failure = RuntimeError("logging client is closed")
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
            return True
        worker.join(timeout=max(timeout, 0.0))
        if worker.is_alive():
            self._fallback_warning(
                f"logging client drain timed out; remaining={self._queue.qsize()}"
            )
            return False
        return True

    async def aclose(self, *, timeout: float = 2.0) -> bool:
        """Drain without blocking an asyncio event loop."""
        worker = self._request_close()
        if worker is None or worker is threading.current_thread():
            return True
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
        return True

    def _ensure_worker_locked(self) -> None:
        if self._worker is not None:
            return
        self._worker = threading.Thread(
            target=self._worker_loop,
            name="stellarmesh-logging-client",
            daemon=True,
        )
        self._worker.start()

    def _request_close(self) -> threading.Thread | None:
        with self._state_lock:
            if not self._closed:
                self._closed = True
                self._stop_requested.set()
                with suppress(queue.Full):
                    self._queue.put_nowait(_STOP_WORKER)
            worker = self._worker
        if worker is None:
            self._close_http_client()
            self._worker_done.set()
        return worker

    def _worker_loop(self) -> None:
        try:
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
        finally:
            self._close_http_client()
            self._worker_done.set()

    def _send_batch(self, events: list[LogEvent]) -> bool:
        payload = json.dumps(
            BatchIngestRequest(events=events).model_dump(mode="json"),
            ensure_ascii=False,
            separators=(",", ":"),
        ).encode()
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
            response = self._get_http_client().post(
                f"{self._base_url}/v1/log-events/batch",
                content=payload,
                headers={
                    "Content-Type": "application/json",
                    "X-Logging-Service-Token": self.config.token,
                },
            )
            response.raise_for_status()
            body = response.json()
            data = body.get("data") if isinstance(body, dict) else None
            if (
                not isinstance(body, dict)
                or body.get("code") != 202
                or not isinstance(data, dict)
                or data.get("accepted") != len(events)
            ):
                raise ValueError("logging service returned an invalid accepted count")
        except Exception as exc:  # noqa: BLE001 - logging must not break callers.
            for event in events:
                self._drop(event, exc)
            return False
        return True

    def _get_http_client(self) -> httpx.Client:
        if self._http_client is None or self._http_client.is_closed:
            self._http_client = httpx.Client(
                timeout=self._timeout,
                transport=self._transport,
                trust_env=False,
            )
        return self._http_client

    def _close_http_client(self) -> None:
        if self._http_client is not None:
            self._http_client.close()
            self._http_client = None

    def _drop(self, event: LogEvent | None, exc: Exception) -> None:
        if self.config.drop_handler is not None:
            self.config.drop_handler(event, exc)
            return
        self._fallback_warning(str(exc))

    def _fallback_warning(self, message: str) -> None:
        now = time.monotonic()
        if now - self._last_fallback_warning < 30:
            return
        self._last_fallback_warning = now
        print(f"[stellarmesh-logging-fallback] {message}", file=sys.stderr)
