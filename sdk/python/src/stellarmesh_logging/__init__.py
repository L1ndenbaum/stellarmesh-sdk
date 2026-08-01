"""Stellarmesh logging SDK public API."""

from .client import Client, ClientConfig, DropHandler, TraceIDProvider
from .codec import decode_event, encode_event
from .contracts import (
    LOG_EVENT_TOPIC,
    BatchIngestRequest,
    IngestRequest,
    IngestResult,
    Level,
    LogEvent,
    should_emit_level,
)
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
    "BatchIngestRequest",
    "Client",
    "ClientConfig",
    "DropHandler",
    "IngestRequest",
    "IngestResult",
    "Level",
    "LogEvent",
    "Logger",
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
