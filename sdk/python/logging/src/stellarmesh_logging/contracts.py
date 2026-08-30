"""规范日志 v2 模型。"""

from __future__ import annotations

import base64
import binascii
from datetime import UTC, datetime
from enum import StrEnum
from typing import Any, Literal
from uuid import UUID, uuid4

from pydantic import BaseModel, ConfigDict, Field, field_serializer, field_validator

from .sanitizer import sanitize_metadata

LOG_EVENT_TOPIC = "stellarmesh.logging.events.v2"
LOG_DEAD_LETTER_TOPIC = "stellarmesh.logging.events.v2.dlq"
MAX_EVENT_JSON_BYTES = 900 * 1024
MAX_HTTP_BODY_BYTES = 1 << 20
MAX_KAFKA_KEY_VALUE_BYTES = 960 * 1024
MAX_KAFKA_MESSAGE_BYTES = 1 << 20


class EventKind(StrEnum):
    """日志事件的用途分类。"""

    LOG = "LOG"
    AUDIT = "AUDIT"


class Level(StrEnum):
    """日志契约接受的严重级别。"""

    DEBUG = "DEBUG"
    INFO = "INFO"
    WARNING = "WARNING"
    ERROR = "ERROR"


_LEVEL_ORDER = {level: index for index, level in enumerate(Level)}


class ContractModel(BaseModel):
    """匹配 additionalProperties=false 契约的基础模型。"""

    model_config = ConfigDict(extra="forbid")


class LogEvent(ContractModel):
    """规范日志 v2 事件。"""

    event_id: str = Field(default_factory=lambda: str(uuid4()))
    timestamp: datetime = Field(default_factory=lambda: datetime.now(UTC))
    kind: EventKind = EventKind.LOG
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
        text = str(value)
        if not text.strip():
            raise ValueError("value must not be empty")
        return text

    @field_validator("service")
    @classmethod
    def _require_trimmed_service(cls, value: str) -> str:
        if value != value.strip():
            raise ValueError("service must be trimmed")
        return value

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
        sanitized = sanitize_metadata({} if value is None else value)
        if not isinstance(sanitized, dict):
            raise ValueError("metadata must be a mapping")
        return sanitized

    @field_serializer("timestamp", when_used="json")
    def _serialize_timestamp(self, value: datetime) -> str:
        return value.isoformat().replace("+00:00", "Z")


class IngestRequest(ContractModel):
    """单事件 HTTP 请求。"""

    event: LogEvent


class BatchIngestRequest(ContractModel):
    """批量 HTTP 请求。"""

    events: list[LogEvent]


class IngestResult(ContractModel):
    """接收服务返回的已接受事件数。"""

    accepted: int


class DeadLetter(ContractModel):
    """包含稳定来源坐标的 Kafka 拒绝消息。"""

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
    """无法写入 DLQ v1 的超大 Kafka 源消息摘要。"""

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
    """规范化调用方提供的日志级别。"""
    if isinstance(value, Level):
        return value
    raw = str(value or Level.INFO.value).strip().upper()
    if raw == "WARN":
        raw = Level.WARNING.value
    try:
        return Level(raw)
    except ValueError as exc:
        raise ValueError("level must be one of DEBUG, INFO, WARNING, ERROR") from exc


def should_emit_level(level: object, minimum_level: object) -> bool:
    """判断日志级别是否达到最低严重级别。"""
    return (
        _LEVEL_ORDER[normalize_level(level)]
        >= _LEVEL_ORDER[normalize_level(minimum_level)]
    )
