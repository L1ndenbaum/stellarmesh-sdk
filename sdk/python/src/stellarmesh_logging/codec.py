"""JSON codec for canonical logging events."""

from __future__ import annotations

import json
from typing import Any

from .contracts import LogEvent


def encode_event(event: LogEvent | dict[str, Any]) -> bytes:
    """Encode one event as compact UTF-8 JSON."""
    payload = event.model_dump(mode="json") if isinstance(event, LogEvent) else event
    return json.dumps(payload, ensure_ascii=False, separators=(",", ":")).encode()


def decode_event(payload: bytes | str) -> LogEvent:
    """Decode and validate one event JSON object."""
    raw = payload.decode() if isinstance(payload, bytes) else payload
    data = json.loads(raw)
    if not isinstance(data, dict):
        raise ValueError("log event payload must be a JSON object")
    return LogEvent.model_validate(data)
