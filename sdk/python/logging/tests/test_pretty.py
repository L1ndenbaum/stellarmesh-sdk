from __future__ import annotations

import io
import json
import logging
from pathlib import Path

import pytest

from stellarmesh_logging import JSONFormatter, PrettyFormatter


def record(**fields: object) -> logging.LogRecord:
    return logging.makeLogRecord(
        {"name": "orders", "levelname": "INFO", "msg": "你好", **fields}
    )


def test_pretty_preserves_shared_contract_fields_without_changing_input() -> None:
    source = (
        Path(__file__).resolve().parents[4]
        / "contracts/logging/sanitization-cases.json"
    )
    for case in json.loads(source.read_text()):
        options = {
            "static_fields": {
                field["key"]: field["value"] for field in case.get("static_fields", [])
            },
            **case.get("options", {}),
        }
        entry = record(**{field["key"]: field["value"] for field in case["attrs"]})
        original = entry.__dict__.copy()
        output = PrettyFormatter(**options).format(entry)
        for key, value in case["want"].items():
            encoded = json.dumps(value, ensure_ascii=False, separators=(",", ":"))
            assert (
                f"{key}={encoded}" in output
                or f"{json.dumps(key, ensure_ascii=False)}={encoded}" in output
            ), case["name"]
        assert entry.__dict__ == original
        assert "你好" in output
        assert "orders" in output


def test_pretty_bounds_values_and_safely_displays_exceptions() -> None:
    try:
        raise ValueError("输入错误\x1b[31m")
    except ValueError as error:
        entry = record(
            msg="多行\n消息\x1b[0m\x85",
            exc_info=(type(error), error, error.__traceback__),
            secret="do-not-print",
            nested={"password": "hidden", "value": object()},
            long_value="x" * 100,
        )
    output = PrettyFormatter(
        max_string_bytes=48, max_message_bytes=32, include_source=True
    ).format(entry)
    payload = json.loads(
        JSONFormatter(
            max_string_bytes=48, max_message_bytes=32, include_source=True
        ).format(entry)
    )
    assert "do-not-print" not in output and "hidden" not in output
    assert "[REDACTED]" in output and "[UNSERIALIZABLE]" in output
    assert payload["long_value"] in output and "[TRUNCATED]" in output
    assert "ValueError" in output and "输入错误" in output
    assert "\n" in output and "\\n" in output
    assert "\x1b" not in output and "\x85" not in output
    assert "source=" in output


@pytest.mark.parametrize("formatter_type", [JSONFormatter, PrettyFormatter])
def test_format_does_not_own_filtering_or_stream(
    formatter_type: type[JSONFormatter] | type[PrettyFormatter],
) -> None:
    stream = io.StringIO()
    handler = logging.StreamHandler(stream)
    handler.setFormatter(formatter_type(static_fields={"service": "orders"}))
    logger = logging.Logger("consumer", level=logging.WARNING)
    logger.addHandler(handler)
    logger.info("filtered-marker")
    logger.error("visible-marker")
    assert "filtered-marker" not in stream.getvalue()
    assert stream.getvalue().count("visible-marker") == 1
    assert not stream.closed
