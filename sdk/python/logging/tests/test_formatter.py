from __future__ import annotations

import io
import json
import logging
import math

import pytest

from stellarmesh_logging import JSONFormatter, __version__


class BrokenException(Exception):
    def __str__(self) -> str:
        raise RuntimeError("secret from __str__")


def _record(
    message: object = "request completed",
    *,
    level: int = logging.INFO,
    args: tuple[object, ...] = (),
    extra: dict[str, object] | None = None,
    exc_info: tuple[object, object, object] | None = None,
) -> logging.LogRecord:
    record = logging.LogRecord(
        name="kgraph.backend",
        level=level,
        pathname="/app/example.py",
        lineno=42,
        msg=message,
        args=args,
        exc_info=exc_info,  # type: ignore[arg-type]
    )
    for key, value in (extra or {}).items():
        setattr(record, key, value)
    return record


def test_version_and_basic_record_are_stable_single_line_json() -> None:
    assert __version__ == "0.4.0"
    formatter = JSONFormatter(static_fields={"service": "backend"})

    payload = formatter.format(
        _record(
            "你好\n%s",
            args=("world",),
            extra={"request_id": "request-1", "duration_ms": 12.5},
        )
    )

    assert "\n" not in payload
    decoded = json.loads(payload)
    assert decoded == {
        "time": decoded["time"],
        "level": "INFO",
        "msg": "你好\nworld",
        "logger": "kgraph.backend",
        "service": "backend",
        "request_id": "request-1",
        "duration_ms": 12.5,
    }
    assert decoded["time"].endswith("Z")


def test_sensitive_nested_fields_and_unsafe_values_are_sanitized() -> None:
    formatter = JSONFormatter(extra_sensitive_keys={"tenant credential"})
    metadata = {
        "apiKey": "api-secret",
        "nested": {
            "Authorization": "Bearer token",
            "tenant-credential": "credential-secret",
        },
        "ratio": math.inf,
        "object": object(),
        "raw": b"bytes",
    }

    decoded = json.loads(formatter.format(_record(extra={"metadata": metadata})))

    assert decoded["metadata"] == {
        "apiKey": "[REDACTED]",
        "nested": {
            "Authorization": "[REDACTED]",
            "tenant-credential": "[REDACTED]",
        },
        "ratio": "[UNSERIALIZABLE]",
        "object": "[UNSERIALIZABLE]",
        "raw": "<bytes:5>",
    }
    assert metadata["apiKey"] == "api-secret"


def test_message_strings_depth_and_attribute_count_are_bounded() -> None:
    formatter = JSONFormatter(
        max_message_bytes=16,
        max_string_bytes=16,
        max_attributes=4,
        max_depth=2,
    )
    decoded = json.loads(
        formatter.format(
            _record(
                "abcdefghijklmnopqrstu",
                extra={
                    "long": "abcdefghijklmnopqrstu",
                    "nested": {"deeper": {"value": "hidden"}},
                    "last": "omitted",
                },
            )
        )
    )

    assert decoded["msg"] == "abcde[TRUNCATED]"
    assert decoded["long"] == "abcde[TRUNCATED]"
    assert decoded["nested"] == {"deeper": {"value": "[TRUNCATED]"}}
    assert "last" not in decoded


def test_exception_and_source_are_structured() -> None:
    formatter = JSONFormatter(include_source=True)
    try:
        raise ValueError("bad value")
    except ValueError as error:
        decoded = json.loads(
            formatter.format(
                _record(exc_info=(type(error), error, error.__traceback__))
            )
        )

    assert decoded["source"] == {
        "file": "/app/example.py",
        "line": 42,
        "function": None,
    }
    assert decoded["exception"]["type"] == "ValueError"
    assert decoded["exception"]["message"] == "bad value"
    assert "ValueError: bad value" in decoded["exception"]["traceback"]


def test_unsafe_exception_and_recursive_metadata_fail_safely() -> None:
    recursive: dict[str, object] = {}
    recursive["self"] = recursive
    formatter = JSONFormatter()
    decoded = json.loads(
        formatter.format(
            _record(
                extra={
                    "error": BrokenException(),
                    "recursive": recursive,
                }
            )
        )
    )

    assert decoded["error"] == "[UNSERIALIZABLE]"
    assert decoded["recursive"] == {"self": "[UNSERIALIZABLE]"}


def test_reserved_fields_cannot_be_overridden() -> None:
    with pytest.raises(ValueError, match="保留字段"):
        JSONFormatter(static_fields={"msg": "override"})

    formatter = JSONFormatter()
    record = _record()
    record.__dict__["level"] = "OVERRIDE"
    decoded = json.loads(formatter.format(record))
    assert decoded["level"] == "INFO"


@pytest.mark.parametrize(
    ("keyword", "value"),
    [
        ("max_message_bytes", 1),
        ("max_string_bytes", 1),
        ("max_attributes", 0),
        ("max_depth", -1),
        ("max_depth", True),
    ],
)
def test_invalid_limits_fail_fast(keyword: str, value: object) -> None:
    with pytest.raises(ValueError):
        JSONFormatter(**{keyword: value})  # type: ignore[arg-type]


def test_invalid_extra_sensitive_key_fails_fast() -> None:
    with pytest.raises(ValueError, match="字母或数字"):
        JSONFormatter(extra_sensitive_keys={"---"})


def test_standard_logger_and_handler_own_filtering_and_stream_lifecycle() -> None:
    stream = io.StringIO()
    handler = logging.StreamHandler(stream)
    handler.setLevel(logging.WARNING)
    handler.setFormatter(JSONFormatter())
    logger = logging.Logger("consumer", level=logging.DEBUG)
    logger.addHandler(handler)

    logger.info("filtered")
    logger.error("written", extra={"request_id": "request-2"})

    lines = stream.getvalue().splitlines()
    assert len(lines) == 1
    assert json.loads(lines[0])["request_id"] == "request-2"
    handler.close()
    assert not stream.closed
