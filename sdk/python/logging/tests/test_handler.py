from __future__ import annotations

import json
import logging
import threading
from collections.abc import Callable
from datetime import UTC, datetime
from typing import Any

import httpx

from stellarmesh_logging import Client, ClientConfig, Level, StellarmeshHandler


def _response(accepted: int) -> httpx.Response:
    return httpx.Response(
        202,
        json={
            "code": 202,
            "message": "accepted",
            "data": {"accepted": accepted},
            "timestamp": "2026-08-01T12:00:00Z",
        },
    )


def _capturing_client(
    captured: list[dict[str, Any]],
    *,
    minimum_level: Level = Level.DEBUG,
    trace_id_provider: Callable[[], str] | None = None,
) -> Client:
    def handle(request: httpx.Request) -> httpx.Response:
        events = json.loads(request.content)["events"]
        captured.extend(events)
        return _response(len(events))

    return Client(
        ClientConfig(
            base_url="http://logging-service",
            token="token",
            service="backend",
            minimum_level=minimum_level,
            batch_size=1,
            trace_id_provider=trace_id_provider,
        ),
        transport=httpx.MockTransport(handle),
    )


def test_handler_converts_formatted_record_and_extra_metadata() -> None:
    captured: list[dict[str, Any]] = []
    client = _capturing_client(captured)
    handler = StellarmeshHandler(client, service="worker")
    logger = logging.getLogger("test.handler.formatted")
    logger.setLevel(logging.INFO)
    logger.addHandler(handler)
    logger.propagate = False

    try:
        logger.info(
            "job %s started",
            "job-1",
            extra={
                "trace_id": "request-1",
                "job_id": "job-1",
                "api_token": "secret",
            },
        )
        assert client.close(timeout=1)
    finally:
        logger.removeHandler(handler)

    assert captured[0]["level"] == "INFO"
    assert captured[0]["service"] == "worker"
    assert captured[0]["message"] == "job job-1 started"
    assert captured[0]["trace_id"] == "request-1"
    metadata = captured[0]["metadata"]
    assert isinstance(metadata.pop("line"), int)
    assert metadata == {
        "api_token": "[REDACTED]",
        "function": "test_handler_converts_formatted_record_and_extra_metadata",
        "job_id": "job-1",
        "logger": "test.handler.formatted",
        "module": "test_handler",
    }


def test_handler_maps_levels_and_preserves_record_timestamp() -> None:
    captured: list[dict[str, Any]] = []
    client = _capturing_client(captured)
    handler = StellarmeshHandler(client)
    expected_timestamp = datetime(2026, 8, 12, 12, 0, tzinfo=UTC)

    records = [
        logging.LogRecord("levels", logging.DEBUG, __file__, 1, "debug", (), None),
        logging.LogRecord("levels", logging.INFO, __file__, 2, "info", (), None),
        logging.LogRecord("levels", logging.WARNING, __file__, 3, "warning", (), None),
        logging.LogRecord("levels", logging.ERROR, __file__, 4, "error", (), None),
        logging.LogRecord(
            "levels", logging.CRITICAL, __file__, 5, "critical", (), None
        ),
        logging.LogRecord("levels", 45, __file__, 6, "audit", (), None),
    ]
    for record in records:
        record.created = expected_timestamp.timestamp()
        handler.emit(record)
    assert client.close(timeout=1)

    assert [event["level"] for event in captured] == [
        "DEBUG",
        "INFO",
        "WARNING",
        "ERROR",
        "ERROR",
        "ERROR",
    ]
    assert {event["kind"] for event in captured} == {"LOG"}
    assert {event["timestamp"] for event in captured} == {"2026-08-12T12:00:00Z"}


def test_handler_uses_provider_and_captures_exception() -> None:
    captured: list[dict[str, Any]] = []
    client = _capturing_client(
        captured,
        trace_id_provider=lambda: "request-from-provider",
    )
    handler = StellarmeshHandler(client)
    logger = logging.getLogger("test.handler.exception")
    logger.setLevel(logging.ERROR)
    logger.addHandler(handler)
    logger.propagate = False

    try:
        try:
            raise RuntimeError("example failure")
        except RuntimeError:
            logger.exception("job failed")
        assert client.close(timeout=1)
    finally:
        logger.removeHandler(handler)

    event = captured[0]
    assert event["trace_id"] == "request-from-provider"
    assert event["metadata"]["exception_type"] == "RuntimeError"
    assert event["metadata"]["exception_message"] == "example failure"
    assert "RuntimeError: example failure" in event["metadata"]["traceback"]


def test_handler_reuses_client_minimum_level_filter() -> None:
    captured: list[dict[str, Any]] = []
    client = _capturing_client(captured, minimum_level=Level.ERROR)
    handler = StellarmeshHandler(client)
    logger = logging.getLogger("test.handler.filter")
    logger.setLevel(logging.DEBUG)
    logger.addHandler(handler)
    logger.propagate = False

    try:
        logger.debug("debug")
        logger.info("info")
        logger.warning("warning")
        logger.error("error")
        assert client.close(timeout=1)
    finally:
        logger.removeHandler(handler)

    assert [event["message"] for event in captured] == ["error"]


def test_handler_does_not_raise_for_closed_client_or_bad_formatting() -> None:
    class RecordingHandler(StellarmeshHandler):
        def __init__(self, client: Client) -> None:
            super().__init__(client)
            self.errors: list[logging.LogRecord] = []

        def handleError(self, record: logging.LogRecord) -> None:  # noqa: N802
            self.errors.append(record)

    dropped: list[Exception] = []
    client = Client(
        ClientConfig(
            base_url="http://logging-service",
            token="token",
            service="backend",
            drop_handler=lambda _event, error: dropped.append(error),
        )
    )
    handler = RecordingHandler(client)
    logger = logging.getLogger("test.handler.failure")
    logger.setLevel(logging.ERROR)
    logger.addHandler(handler)
    logger.propagate = False

    try:
        assert client.close(timeout=1)
        logger.error("closed client")
        logger.error("invalid integer: %d", "text")
    finally:
        logger.removeHandler(handler)

    assert len(dropped) == 1
    assert "closed" in str(dropped[0])
    assert len(handler.errors) == 1


def test_handler_does_not_raise_when_client_queue_is_full() -> None:
    started = threading.Event()
    release = threading.Event()
    dropped: list[Exception] = []

    def handle(_: httpx.Request) -> httpx.Response:
        started.set()
        assert release.wait(timeout=1)
        return _response(1)

    client = Client(
        ClientConfig(
            base_url="http://logging-service",
            token="token",
            service="backend",
            queue_size=1,
            batch_size=1,
            drop_handler=lambda _event, error: dropped.append(error),
        ),
        transport=httpx.MockTransport(handle),
    )
    handler = StellarmeshHandler(client)
    logger = logging.getLogger("test.handler.queue-full")
    logger.setLevel(logging.ERROR)
    logger.addHandler(handler)
    logger.propagate = False

    try:
        logger.error("first")
        assert started.wait(timeout=1)
        logger.error("second")
        release.set()
        assert client.close(timeout=1)
    finally:
        logger.removeHandler(handler)

    assert len(dropped) == 1
    assert "queue is full" in str(dropped[0])
