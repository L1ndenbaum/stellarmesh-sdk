"""Canonical logging v1 models."""

from __future__ import annotations

import base64
import binascii
from datetime import UTC, datetime
from enum import StrEnum
from typing import Any, Literal
from uuid import UUID, uuid4

from pydantic import BaseModel, ConfigDict, Field, field_serializer, field_validator

from .sanitizer import sanitize_metadata

LOG_EVENT_TOPIC = "stellarmesh.logging.events.v1"
LOG_DEAD_LETTER_TOPIC = "stellarmesh.logging.events.v1.dlq"
MAX_EVENT_JSON_BYTES = 900 * 1024
MAX_HTTP_BODY_BYTES = 1 << 20
MAX_KAFKA_MESSAGE_BYTES = 1 << 20


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


class DeadLetter(ContractModel):
    """Rejected Kafka payload with stable source coordinates."""

    schema_version: Literal["v1"]
    source_topic: str = Field(min_length=1)
    source_partition: int = Field(ge=0)
    source_offset: int = Field(ge=0)
    source_timestamp: datetime | None = None
    source_key_base64: str
    reason: Literal["invalid_event"]
    error: str = Field(min_length=1, max_length=2048)
    payload_base64: str
    failed_at: datetime

    @field_validator("source_topic", "error")
    @classmethod
    def _require_dead_letter_text(cls, value: str) -> str:
        if not value.strip():
            raise ValueError("value must not be empty")
        return value

    @field_validator("source_key_base64", "payload_base64")
    @classmethod
    def _validate_base64(cls, value: str) -> str:
        try:
            decoded = base64.b64decode(value, validate=True)
        except (binascii.Error, ValueError) as exc:
            raise ValueError("value must be canonical base64") from exc
        if base64.b64encode(decoded).decode() != value:
            raise ValueError("value must be canonical base64")
        return value

    @field_validator("source_timestamp", "failed_at", mode="after")
    @classmethod
    def _ensure_optional_timezone(cls, value: datetime | None) -> datetime | None:
        if value is None:
            return None
        if value.tzinfo is None:
            raise ValueError("timestamp must include a timezone")
        normalized = value.astimezone(UTC)
        if normalized == datetime.min.replace(tzinfo=UTC):
            raise ValueError("timestamp must not be zero")
        return normalized

    @field_serializer("source_timestamp", "failed_at", when_used="json")
    def _serialize_dead_letter_timestamp(self, value: datetime | None) -> str | None:
        if value is None:
            return None
        return value.isoformat().replace("+00:00", "Z")


class OversizeDeadLetter(ContractModel):
    """Compact digest for a Kafka source message that is too large for DLQ v1."""

    schema_version: Literal["v2"]
    source_topic: str = Field(min_length=1)
    source_partition: int = Field(ge=0)
    source_offset: int = Field(ge=0)
    source_timestamp: datetime | None = None
    reason: Literal["source_message_too_large"]
    error: str = Field(min_length=1, max_length=2048)
    source_key_bytes: int = Field(ge=0)
    source_key_sha256: str = Field(pattern=r"^[0-9a-f]{64}$")
    payload_bytes: int = Field(ge=0)
    payload_sha256: str = Field(pattern=r"^[0-9a-f]{64}$")
    content_omitted: Literal[True]
    failed_at: datetime

    @field_validator("source_topic", "error")
    @classmethod
    def _require_oversize_dead_letter_text(cls, value: str) -> str:
        if not value.strip():
            raise ValueError("value must not be empty")
        return value

    @field_validator("source_timestamp", "failed_at", mode="after")
    @classmethod
    def _ensure_oversize_timestamp(cls, value: datetime | None) -> datetime | None:
        if value is None:
            return None
        if value.tzinfo is None:
            raise ValueError("timestamp must include a timezone")
        normalized = value.astimezone(UTC)
        if normalized == datetime.min.replace(tzinfo=UTC):
            raise ValueError("timestamp must not be zero")
        return normalized

    @field_serializer("source_timestamp", "failed_at", when_used="json")
    def _serialize_oversize_timestamp(self, value: datetime | None) -> str | None:
        if value is None:
            return None
        return value.isoformat().replace("+00:00", "Z")


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
