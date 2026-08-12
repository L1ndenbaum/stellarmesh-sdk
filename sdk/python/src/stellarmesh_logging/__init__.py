"""Stellarmesh 日志 SDK 的公共 API。"""

from .client import Client, ClientConfig, DropHandler, TraceIDProvider
from .codec import decode_event, encode_event
from .contracts import (
    LOG_DEAD_LETTER_TOPIC,
    LOG_EVENT_TOPIC,
    MAX_EVENT_JSON_BYTES,
    MAX_HTTP_BODY_BYTES,
    MAX_KAFKA_KEY_VALUE_BYTES,
    MAX_KAFKA_MESSAGE_BYTES,
    BatchIngestRequest,
    DeadLetter,
    IngestRequest,
    IngestResult,
    Level,
    LogEvent,
    OversizeDeadLetter,
    should_emit_level,
)
from .handler import StellarmeshHandler
from .logger import (
    Logger,
    get_logger,
    set_default_client,
    shutdown_logging,
    shutdown_logging_sync,
)
from .sanitizer import sanitize_metadata

__all__ = [
    "LOG_EVENT_TOPIC",
    "LOG_DEAD_LETTER_TOPIC",
    "MAX_EVENT_JSON_BYTES",
    "MAX_HTTP_BODY_BYTES",
    "MAX_KAFKA_KEY_VALUE_BYTES",
    "MAX_KAFKA_MESSAGE_BYTES",
    "BatchIngestRequest",
    "Client",
    "ClientConfig",
    "DropHandler",
    "DeadLetter",
    "IngestRequest",
    "IngestResult",
    "Level",
    "LogEvent",
    "OversizeDeadLetter",
    "Logger",
    "StellarmeshHandler",
    "TraceIDProvider",
    "decode_event",
    "encode_event",
    "get_logger",
    "sanitize_metadata",
    "set_default_client",
    "should_emit_level",
    "shutdown_logging",
    "shutdown_logging_sync",
]
