"""Canonical logging v1 models."""

from __future__ import annotations

from datetime import UTC, datetime
from enum import StrEnum
from typing import Any
from uuid import UUID, uuid4

from pydantic import BaseModel, ConfigDict, Field, field_serializer, field_validator

from .sanitizer import sanitize_metadata

LOG_EVENT_TOPIC = "stellarmesh.logging.events.v1"


class Level(StrEnum):
    """Severity values accepted by the logging contract."""

    DEBUG = "DEBUG"
    INFO = "INFO"
    WARNING = "WARNING"
    ERROR = "ERROR"
    AUDIT = "AUDIT"


_LEVEL_ORDER = {level: index for index, level in enumerate(Level)}


class ContractModel(BaseModel):
    """Base model that matches additionalProperties=false contracts."""

    model_config = ConfigDict(extra="forbid")


class LogEvent(ContractModel):
    """Canonical logging v1 event."""

    event_id: str = Field(default_factory=lambda: str(uuid4()))
    timestamp: datetime = Field(default_factory=lambda: datetime.now(UTC))
    level: Level = Level.INFO
    service: str
    message: str
    trace_id: str = ""
    metadata: dict[str, Any] = Field(default_factory=dict)

    @field_validator("event_id", mode="before")
    @classmethod
    def _canonical_event_id(cls, value: object) -> str:
        raw = str(value)
        canonical = str(UUID(raw))
        if raw != canonical:
            raise ValueError("event_id must be a canonical lowercase UUID")
        return canonical

    @field_validator("service", "message")
    @classmethod
    def _require_text(cls, value: str) -> str:
        text = str(value).strip()
        if not text:
            raise ValueError("value must not be empty")
        return text

    @field_validator("timestamp", mode="after")
    @classmethod
    def _ensure_timezone(cls, value: datetime) -> datetime:
        if value.tzinfo is None:
            raise ValueError("timestamp must include a timezone")
        return value.astimezone(UTC)

    @field_validator("level", mode="before")
    @classmethod
    def _normalize_level(cls, value: object) -> Level:
        return normalize_level(value)

    @field_validator("metadata", mode="before")
    @classmethod
    def _sanitize_metadata(cls, value: Any) -> dict[str, Any]:
        sanitized = sanitize_metadata(value or {})
        if not isinstance(sanitized, dict):
            raise ValueError("metadata must be a mapping")
        return sanitized

    @field_serializer("timestamp", when_used="json")
    def _serialize_timestamp(self, value: datetime) -> str:
        return value.isoformat().replace("+00:00", "Z")


class IngestRequest(ContractModel):
    """Single-event HTTP request."""

    event: LogEvent


class BatchIngestRequest(ContractModel):
    """Batch HTTP request."""

    events: list[LogEvent]


class IngestResult(ContractModel):
    """Accepted event count returned by the ingester."""

    accepted: int


def normalize_level(value: object) -> Level:
    """Normalize a caller-provided level value."""
    if isinstance(value, Level):
        return value
    raw = str(value or Level.INFO.value).strip().upper()
    if raw == "WARN":
        raw = Level.WARNING.value
    try:
        return Level(raw)
    except ValueError as exc:
        raise ValueError(
            "level must be one of DEBUG, INFO, WARNING, ERROR, AUDIT"
        ) from exc


def should_emit_level(level: object, minimum_level: object) -> bool:
    """Return whether level meets a minimum severity."""
    return (
        _LEVEL_ORDER[normalize_level(level)]
        >= _LEVEL_ORDER[normalize_level(minimum_level)]
    )
