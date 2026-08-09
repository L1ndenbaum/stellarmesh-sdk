"""Metadata redaction and size bounding."""

from __future__ import annotations

import math
from collections.abc import Mapping, Sequence
from dataclasses import fields, is_dataclass
from datetime import date, datetime
from enum import Enum
from typing import Any
from uuid import UUID

_SENSITIVE_KEY_PARTS = (
    "api_key",
    "authorization",
    "cookie",
    "credential",
    "jwt",
    "password",
    "secret",
    "token",
)
_MAX_STRING_LENGTH = 2048
_MAX_DEPTH = 6
_MAX_SEQUENCE_LENGTH = 50


def sanitize_metadata(
    value: Any,
    *,
    _depth: int = 0,
    _seen: set[int] | None = None,
) -> Any:
    """Remove likely secrets and bound recursively nested metadata."""
    if _depth > _MAX_DEPTH:
        return "[MAX_DEPTH]"
    seen = _seen if _seen is not None else set()
    if isinstance(value, Mapping):
        if id(value) in seen:
            return "[UNSERIALIZABLE]"
        seen.add(id(value))
        sanitized: dict[str, Any] = {}
        try:
            for raw_key, raw_value in value.items():
                key = str(raw_key)
                sanitized[key] = (
                    "[REDACTED]"
                    if _is_sensitive_key(key)
                    else sanitize_metadata(
                        raw_value,
                        _depth=_depth + 1,
                        _seen=seen,
                    )
                )
            return sanitized
        finally:
            seen.remove(id(value))
    if isinstance(value, str):
        if len(value) > _MAX_STRING_LENGTH:
            return value[:_MAX_STRING_LENGTH] + "...[TRUNCATED]"
        return value
    if isinstance(value, bytes):
        return f"<bytes:{len(value)}>"
    if isinstance(value, Sequence) and not isinstance(value, (str, bytes, bytearray)):
        if id(value) in seen:
            return "[UNSERIALIZABLE]"
        seen.add(id(value))
        try:
            items = [
                sanitize_metadata(
                    item,
                    _depth=_depth + 1,
                    _seen=seen,
                )
                for item in list(value)[:_MAX_SEQUENCE_LENGTH]
            ]
            if len(value) > _MAX_SEQUENCE_LENGTH:
                items.append(f"...[{len(value) - _MAX_SEQUENCE_LENGTH} more]")
            return items
        finally:
            seen.remove(id(value))
    if is_dataclass(value) and not isinstance(value, type):
        return sanitize_metadata(
            {field.name: getattr(value, field.name) for field in fields(value)},
            _depth=_depth,
            _seen=seen,
        )
    model_dump = getattr(value, "model_dump", None)
    if callable(model_dump):
        try:
            dumped = model_dump(mode="python")
        except Exception:  # noqa: BLE001 - metadata must fail closed.
            return "[UNSERIALIZABLE]"
        return sanitize_metadata(dumped, _depth=_depth, _seen=seen)
    if isinstance(value, Enum):
        return sanitize_metadata(value.value, _depth=_depth, _seen=seen)
    if isinstance(value, (datetime, date, UUID)):
        return str(value)
    if value is None or isinstance(value, (bool, int)):
        return value
    if isinstance(value, float):
        return value if math.isfinite(value) else "[UNSERIALIZABLE]"
    return "[UNSERIALIZABLE]"


def _is_sensitive_key(key: str) -> bool:
    normalized = key.lower().replace("-", "_")
    return any(part in normalized for part in _SENSITIVE_KEY_PARTS)
