from __future__ import annotations

import json
import logging
from collections.abc import Iterator, Mapping, Sequence
from dataclasses import dataclass
from datetime import UTC, date, datetime
from enum import Enum
from pathlib import Path
from typing import Any
from uuid import UUID

import pytest

from stellarmesh_logging import JSONFormatter


def record(extra: dict[str, object]) -> logging.LogRecord:
    return logging.makeLogRecord(
        {"name": "test", "levelname": "INFO", "msg": "contract", **extra}
    )


def test_shared_sanitization_contract() -> None:
    source = (
        Path(__file__).resolve().parents[4]
        / "contracts/logging/sanitization-cases.json"
    )
    cases = json.loads(source.read_text())
    for case in cases:
        formatter = JSONFormatter(
            static_fields={
                field["key"]: field["value"] for field in case.get("static_fields", [])
            },
            **case.get("options", {}),
        )
        payload = json.loads(
            formatter.format(
                record({field["key"]: field["value"] for field in case["attrs"]})
            )
        )
        for key in ("time", "level", "msg", "logger"):
            del payload[key]
        assert payload == case["want"], case["name"]


class UnsupportedMapping(Mapping[str, object]):
    def __getitem__(self, key: str) -> object:
        raise AssertionError("业务 Mapping 不应被读取")

    def __iter__(self) -> Iterator[str]:
        raise AssertionError("业务 Mapping 不应被遍历")

    def __len__(self) -> int:
        raise AssertionError("业务 Mapping 不应被读取")


class UnsupportedSequence(Sequence[object]):
    def __getitem__(self, index: Any) -> Any:
        raise AssertionError("业务 Sequence 不应被读取")

    def __len__(self) -> int:
        raise AssertionError("业务 Sequence 不应被读取")


class UnsupportedDict(dict[str, object]):
    def items(self) -> Any:
        raise AssertionError("容器子类不应被遍历")


@dataclass
class BusinessObject:
    value: str

    def __str__(self) -> str:
        raise AssertionError("业务对象不应隐式转为字符串")


def test_unsupported_objects_are_not_evaluated() -> None:
    values = {
        "mapping": UnsupportedMapping(),
        "sequence": UnsupportedSequence(),
        "subclass": UnsupportedDict(value="hidden"),
        "dataclass": BusinessObject("hidden"),
        "object": object(),
    }
    result = json.loads(JSONFormatter().format(record(values)))
    assert {key: result[key] for key in values} == dict.fromkeys(
        values, "[UNSERIALIZABLE]"
    )


class Kind(Enum):
    LOG = "LOG"


def test_common_values_and_input_ownership() -> None:
    fields = {
        "tuple": (1, "two"),
        "date": date(2026, 9, 5),
        "datetime": datetime(2026, 9, 5, tzinfo=UTC),
        "uuid": UUID("00000000-0000-4000-8000-000000000001"),
        "kind": Kind.LOG,
        "metadata": {"Authorization": "hidden"},
    }
    static: dict[str, object] = {"service": "original"}
    formatter = JSONFormatter(static_fields=static)
    static["service"] = "changed"
    result = json.loads(formatter.format(record(fields)))
    assert result["service"] == "original"
    assert result["tuple"] == [1, "two"]
    assert result["date"] == "2026-09-05"
    assert result["datetime"] == "2026-09-05 00:00:00+00:00"
    assert result["uuid"] == str(fields["uuid"])
    assert result["kind"] == "LOG"
    assert result["metadata"] == {"Authorization": "[REDACTED]"}
    assert fields["metadata"] == {"Authorization": "hidden"}


@pytest.mark.parametrize(
    "option", ["max_attributes", "max_depth", "max_string_bytes", "max_message_bytes"]
)
def test_limits_require_integers(option: str) -> None:
    with pytest.raises(ValueError):
        JSONFormatter(**{option: 16.5})  # type: ignore[arg-type]
