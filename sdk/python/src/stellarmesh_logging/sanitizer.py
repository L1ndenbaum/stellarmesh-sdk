"""Metadata redaction and size bounding."""

from __future__ import annotations

from collections.abc import Mapping, Sequence
from typing import Any

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


def sanitize_metadata(value: Any, *, _depth: int = 0) -> Any:
    """Remove likely secrets and bound recursively nested metadata."""
    if _depth > _MAX_DEPTH:
        return "[MAX_DEPTH]"
    if isinstance(value, Mapping):
        sanitized: dict[str, Any] = {}
        for raw_key, raw_value in value.items():
            key = str(raw_key)
            sanitized[key] = (
                "[REDACTED]"
                if _is_sensitive_key(key)
                else sanitize_metadata(raw_value, _depth=_depth + 1)
            )
        return sanitized
    if isinstance(value, str):
        if len(value) > _MAX_STRING_LENGTH:
            return value[:_MAX_STRING_LENGTH] + "...[TRUNCATED]"
        return value
    if isinstance(value, bytes):
        return f"<bytes:{len(value)}>"
    if isinstance(value, Sequence) and not isinstance(value, (str, bytes, bytearray)):
        items = [
            sanitize_metadata(item, _depth=_depth + 1)
            for item in list(value)[:_MAX_SEQUENCE_LENGTH]
        ]
        if len(value) > _MAX_SEQUENCE_LENGTH:
            items.append(f"...[{len(value) - _MAX_SEQUENCE_LENGTH} more]")
        return items
    if value is None or isinstance(value, (bool, int, float)):
        return value
    return str(value)


def _is_sensitive_key(key: str) -> bool:
    normalized = key.lower().replace("-", "_")
    return any(part in normalized for part in _SENSITIVE_KEY_PARTS)
