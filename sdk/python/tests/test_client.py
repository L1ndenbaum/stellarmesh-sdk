from __future__ import annotations

import asyncio
import json
from dataclasses import dataclass
from typing import Any

import httpx
import pytest

from stellarmesh_logging import Client, ClientConfig, Level, LogEvent, get_logger


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


def _legacy_response(accepted: int) -> httpx.Response:
    return httpx.Response(
        200,
        json={
            "code": 200,
            "message": "accepted",
            "data": {"accepted": accepted},
            "timestamp": "2026-08-01T12:00:00Z",
        },
    )


def test_client_batches_and_drains() -> None:
    batches: list[list[str]] = []

    def handle(request: httpx.Request) -> httpx.Response:
        payload = json.loads(request.content)
        batches.append([event["message"] for event in payload["events"]])
        assert request.headers["X-Logging-Service-Token"] == "token"
        return _response(len(payload["events"]))

    client = Client(
        ClientConfig(
            base_url="http://logging-service",
            token="token",
            service="backend",
            batch_size=2,
            flush_interval_ms=10_000,
        ),
        transport=httpx.MockTransport(handle),
    )
    assert client.emit_event(Level.INFO, message="first")
    assert client.emit_event(Level.INFO, message="second")
    assert client.close(timeout=1)
    assert batches == [["first", "second"]]


def test_client_splits_oversized_batch() -> None:
    batches: list[list[str]] = []

    def handle(request: httpx.Request) -> httpx.Response:
        payload = json.loads(request.content)
        batches.append([event["message"] for event in payload["events"]])
        return _response(len(payload["events"]))

    client = Client(
        ClientConfig(
            base_url="http://logging-service",
            token="token",
            service="backend",
            batch_size=2,
            flush_interval_ms=10_000,
            max_body_bytes=700,
        ),
        transport=httpx.MockTransport(handle),
    )
    for message in ("first", "second"):
        assert client.emit_event(
            Level.INFO, message=message, metadata={"payload": "x" * 300}
        )
    assert client.close(timeout=1)
    assert batches == [["first"], ["second"]]


def test_trace_provider_and_logger_redaction() -> None:
    captured: list[dict[str, Any]] = []

    def handle(request: httpx.Request) -> httpx.Response:
        payload = json.loads(request.content)
        captured.extend(payload["events"])
        return _response(len(payload["events"]))

    client = Client(
        ClientConfig(
            base_url="http://logging-service",
            token="token",
            service="backend",
            trace_id_provider=lambda: "request-1",
            batch_size=1,
        ),
        transport=httpx.MockTransport(handle),
    )
    logger = get_logger("example.module", client=client)
    assert logger.info("created", api_token="secret")
    assert client.close(timeout=1)

    assert captured[0]["trace_id"] == "request-1"
    assert captured[0]["metadata"]["api_token"] == "[REDACTED]"


def test_aclose_does_not_block_event_loop() -> None:
    client = Client(
        ClientConfig(
            base_url="http://logging-service",
            token="token",
            service="backend",
        ),
        transport=httpx.MockTransport(lambda _: _response(1)),
    )
    assert client.emit_event(Level.INFO, message="event")
    assert asyncio.run(client.aclose(timeout=1))


def test_client_accepts_legacy_ok_response() -> None:
    client = Client(
        ClientConfig(
            base_url="http://logging-service",
            token="token",
            service="backend",
            batch_size=1,
        ),
        transport=httpx.MockTransport(lambda _: _legacy_response(1)),
    )
    assert client.emit_event(Level.INFO, message="event")
    assert client.close(timeout=1)


def test_client_isolates_provider_and_drop_handler_errors(
    capsys: pytest.CaptureFixture[str],
) -> None:
    def provider() -> str:
        raise RuntimeError("provider failed")

    def drop_handler(_event: Any, _error: Exception) -> None:
        raise RuntimeError("callback failed")

    client = Client(
        ClientConfig(
            base_url="http://logging-service",
            token="token",
            service="backend",
            trace_id_provider=provider,
            drop_handler=drop_handler,
        )
    )
    assert not client.emit_event(Level.INFO, message="event")
    assert "drop handler failed" in capsys.readouterr().err
    assert client.close(timeout=1)


def test_client_rejects_invalid_configuration() -> None:
    invalid = [
        ClientConfig(base_url="://bad", token="token", service="backend"),
        ClientConfig(base_url="http://logging-service", token="", service="backend"),
        ClientConfig(base_url="http://logging-service", token="token", service=""),
        ClientConfig(
            base_url="http://logging-service",
            token="token",
            service="backend",
            queue_size=0,
        ),
    ]
    for config in invalid:
        try:
            Client(config)
        except ValueError:
            continue
        raise AssertionError(f"accepted invalid config: {config}")


def test_typed_metadata_is_redacted_and_unknown_objects_fail_closed() -> None:
    @dataclass
    class Credentials:
        password: str
        safe: str

    event = LogEvent(
        service="backend",
        message="event",
        metadata={
            "typed": Credentials(password="secret", safe="value"),
            "unknown": object(),
        },
    )
    assert event.metadata["typed"] == {"password": "[REDACTED]", "safe": "value"}
    assert event.metadata["unknown"] == "[UNSERIALIZABLE]"
