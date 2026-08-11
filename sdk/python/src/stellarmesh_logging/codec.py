"""JSON codec for canonical logging events."""

from __future__ import annotations

import json
import re
from typing import Any

from .contracts import LogEvent

_EVENT_FIELDS = {
    "event_id",
    "timestamp",
    "level",
    "service",
    "message",
    "trace_id",
    "metadata",
}
_EVENT_LEVELS = {"DEBUG", "INFO", "WARNING", "ERROR", "AUDIT"}
_CANONICAL_TIMESTAMP = re.compile(
    r"^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}"
    r"(?:\.[0-9]+)?(?:Z|[+-][0-9]{2}:[0-9]{2})$"
)


def encode_event(event: LogEvent | dict[str, Any]) -> bytes:
    """Encode one event as compact UTF-8 JSON."""
    payload = event.model_dump(mode="json") if isinstance(event, LogEvent) else event
    return json.dumps(payload, ensure_ascii=False, separators=(",", ":")).encode()


def decode_event(payload: bytes | str) -> LogEvent:
    """Decode and validate one event JSON object."""
    raw = payload.decode() if isinstance(payload, bytes) else payload
    data = json.loads(raw, parse_constant=_reject_json_constant)
    if not isinstance(data, dict):
        raise ValueError("log event payload must be a JSON object")
    missing = _EVENT_FIELDS - data.keys()
    if missing:
        raise ValueError(f"log event fields are required: {', '.join(sorted(missing))}")
    if set(data) != _EVENT_FIELDS:
        unexpected = set(data) - _EVENT_FIELDS
        names = ", ".join(sorted(unexpected))
        raise ValueError(f"unexpected log event fields: {names}")
    for field in ("event_id", "timestamp", "level", "service", "message", "trace_id"):
        if not isinstance(data[field], str):
            raise ValueError(f"log event field {field} must be a string")
    if data["level"] not in _EVENT_LEVELS:
        raise ValueError("log event level must use a canonical uppercase value")
    if _CANONICAL_TIMESTAMP.fullmatch(data["timestamp"]) is None:
        raise ValueError("log event timestamp must use canonical RFC 3339 syntax")
    if not data["service"].strip() or not data["message"].strip():
        raise ValueError(
            "log event service and message must contain non-whitespace text"
        )
    if not isinstance(data["metadata"], dict):
        raise ValueError("log event metadata must be an object")
    return LogEvent.model_validate(data)


def _reject_json_constant(value: str) -> None:
    raise ValueError(f"non-standard JSON constant is not allowed: {value}")
