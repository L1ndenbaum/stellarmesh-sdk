from __future__ import annotations

import json
from pathlib import Path

import pytest
from pydantic import ValidationError

from stellarmesh_logging import (
    Level,
    LogEvent,
    decode_event,
    encode_event,
    should_emit_level,
)


def test_contract_fixture_round_trips() -> None:
    fixture = (
        Path(__file__).parents[3]
        / "contracts"
        / "logging"
        / "v1"
        / "testdata"
        / "valid-event.json"
    )
    payload = fixture.read_bytes()
    event = decode_event(payload)

    assert event.level == Level.INFO
    assert json.loads(encode_event(event))["timestamp"] == "2026-08-01T12:00:00Z"


def test_event_sanitizes_metadata() -> None:
    event = LogEvent(
        service="backend",
        message="created",
        metadata={"api_token": "secret", "nested": {"safe": "value"}},
    )
    assert event.metadata["api_token"] == "[REDACTED]"
    assert event.metadata["nested"] == {"safe": "value"}


def test_event_rejects_invalid_identifier() -> None:
    with pytest.raises(ValidationError):
        LogEvent(event_id="not-a-uuid", service="backend", message="invalid")


def test_level_filtering() -> None:
    assert should_emit_level("WARN", "INFO")
    assert not should_emit_level("DEBUG", "INFO")
    assert should_emit_level("AUDIT", "ERROR")
