"""标准库 logging 的安全单行 JSON Formatter。"""

from __future__ import annotations

import dataclasses
import json
import logging
import math
import re
import traceback
from collections.abc import Collection, Mapping, Sequence
from datetime import UTC, date, datetime
from enum import Enum
from typing import Any
from uuid import UUID

_DEFAULT_MAX_MESSAGE_BYTES = 16 * 1024
_DEFAULT_MAX_STRING_BYTES = 16 * 1024
_DEFAULT_MAX_ATTRIBUTES = 64
_DEFAULT_MAX_DEPTH = 8

_REDACTED = "[REDACTED]"
_UNSERIALIZABLE = "[UNSERIALIZABLE]"
_TRUNCATED = "[TRUNCATED]"

_BUILTIN_SENSITIVE_KEYS = (
    "apikey",
    "authorization",
    "clientsecret",
    "cookie",
    "credential",
    "jwt",
    "password",
    "privatekey",
    "secret",
    "session",
    "token",
)

_RESERVED_OUTPUT_FIELDS = frozenset(
    {"time", "level", "msg", "logger", "source", "exception"}
)
_STANDARD_RECORD_FIELDS = frozenset(
    {
        *logging.makeLogRecord({}).__dict__,
        "asctime",
        "message",
    }
)


class _Budget:
    def __init__(self, remaining: int) -> None:
        self.remaining = remaining

    def consume(self) -> bool:
        if self.remaining <= 0:
            return False
        self.remaining -= 1
        return True


class JSONFormatter(logging.Formatter):
    """把 LogRecord 编码为有界、脱敏的单行 JSON。"""

    def __init__(
        self,
        *,
        static_fields: Mapping[str, object] | None = None,
        extra_sensitive_keys: Collection[str] = (),
        max_message_bytes: int = _DEFAULT_MAX_MESSAGE_BYTES,
        max_string_bytes: int = _DEFAULT_MAX_STRING_BYTES,
        max_attributes: int = _DEFAULT_MAX_ATTRIBUTES,
        max_depth: int = _DEFAULT_MAX_DEPTH,
        include_source: bool = False,
    ) -> None:
        super().__init__()
        self._max_message_bytes = _byte_limit("max_message_bytes", max_message_bytes)
        self._max_string_bytes = _byte_limit("max_string_bytes", max_string_bytes)
        self._max_attributes = _positive_limit("max_attributes", max_attributes)
        self._max_depth = _positive_limit("max_depth", max_depth)
        self._include_source = include_source

        copied_fields = dict(static_fields or {})
        conflicts = _RESERVED_OUTPUT_FIELDS.intersection(copied_fields)
        if conflicts:
            names = ", ".join(sorted(conflicts))
            raise ValueError(f"static_fields 不能覆盖保留字段: {names}")
        self._static_fields = copied_fields

        sensitive_keys = list(_BUILTIN_SENSITIVE_KEYS)
        for key in extra_sensitive_keys:
            normalized = _normalize_key(key)
            if not normalized:
                raise ValueError("extra_sensitive_keys 必须包含字母或数字")
            sensitive_keys.append(normalized)
        self._sensitive_keys = tuple(sensitive_keys)

    def format(self, record: logging.LogRecord) -> str:
        budget = _Budget(self._max_attributes)
        result: dict[str, object] = {
            "time": datetime.fromtimestamp(record.created, UTC)
            .isoformat(timespec="milliseconds")
            .replace("+00:00", "Z"),
            "level": record.levelname,
            "msg": _truncate_utf8(_safe_message(record), self._max_message_bytes),
            "logger": record.name,
        }

        for key, value in self._static_fields.items():
            if not budget.consume():
                break
            result[key] = self._sanitize_keyed(key, value, 1, budget, set())

        for key, value in record.__dict__.items():
            if (
                key in _STANDARD_RECORD_FIELDS
                or key in _RESERVED_OUTPUT_FIELDS
                or key in result
            ):
                continue
            if not budget.consume():
                break
            result[key] = self._sanitize_keyed(key, value, 1, budget, set())

        if self._include_source:
            result["source"] = {
                "file": record.pathname,
                "line": record.lineno,
                "function": record.funcName,
            }
        if record.exc_info:
            result["exception"] = self._exception(record.exc_info)

        try:
            return json.dumps(
                result,
                ensure_ascii=False,
                allow_nan=False,
                separators=(",", ":"),
            )
        except (TypeError, ValueError, OverflowError):
            # 最后一道防线保证异常日志本身不会破坏业务日志调用。
            return json.dumps(
                {
                    "time": result["time"],
                    "level": result["level"],
                    "msg": _UNSERIALIZABLE,
                    "logger": result["logger"],
                },
                ensure_ascii=False,
                separators=(",", ":"),
            )

    def _sanitize_keyed(
        self,
        key: str,
        value: object,
        depth: int,
        budget: _Budget,
        seen: set[int],
    ) -> object:
        if self._is_sensitive_key(key):
            return _REDACTED
        return self._sanitize(value, depth, budget, seen)

    def _sanitize(
        self,
        value: object,
        depth: int,
        budget: _Budget,
        seen: set[int],
    ) -> object:
        if depth > self._max_depth:
            return _TRUNCATED
        if isinstance(value, str):
            return _truncate_utf8(value, self._max_string_bytes)
        if isinstance(value, BaseException):
            return _truncate_utf8(
                _safe_exception_message(value), self._max_string_bytes
            )
        if isinstance(value, bytes):
            return f"<bytes:{len(value)}>"
        if value is None or isinstance(value, (bool, int)):
            return value
        if isinstance(value, float):
            return value if math.isfinite(value) else _UNSERIALIZABLE
        if isinstance(value, Enum):
            return self._sanitize(value.value, depth, budget, seen)
        if isinstance(value, (datetime, date, UUID)):
            return _truncate_utf8(str(value), self._max_string_bytes)
        if dataclasses.is_dataclass(value) and not isinstance(value, type):
            try:
                mapped = {
                    field.name: getattr(value, field.name)
                    for field in dataclasses.fields(value)
                }
            except Exception:  # noqa: BLE001 - 日志数据必须安全失败。
                return _UNSERIALIZABLE
            return self._sanitize(mapped, depth, budget, seen)
        if isinstance(value, Mapping):
            return self._sanitize_mapping(value, depth, budget, seen)
        if isinstance(value, Sequence) and not isinstance(
            value, (str, bytes, bytearray)
        ):
            return self._sanitize_sequence(value, depth, budget, seen)
        return _UNSERIALIZABLE

    def _sanitize_mapping(
        self,
        value: Mapping[object, object],
        depth: int,
        budget: _Budget,
        seen: set[int],
    ) -> object:
        if id(value) in seen:
            return _UNSERIALIZABLE
        seen.add(id(value))
        result: dict[str, object] = {}
        try:
            for raw_key, nested in value.items():
                if not budget.consume():
                    break
                key = _safe_key(raw_key)
                result[key] = self._sanitize_keyed(key, nested, depth + 1, budget, seen)
            return result
        except Exception:  # noqa: BLE001 - 任意 Mapping 实现都不能打断业务流程。
            return _UNSERIALIZABLE
        finally:
            seen.remove(id(value))

    def _sanitize_sequence(
        self,
        value: Sequence[object],
        depth: int,
        budget: _Budget,
        seen: set[int],
    ) -> object:
        if id(value) in seen:
            return _UNSERIALIZABLE
        seen.add(id(value))
        result: list[object] = []
        try:
            for nested in value:
                if not budget.consume():
                    break
                result.append(self._sanitize(nested, depth + 1, budget, seen))
            return result
        except Exception:  # noqa: BLE001 - 任意 Sequence 实现都不能打断业务流程。
            return _UNSERIALIZABLE
        finally:
            seen.remove(id(value))

    def _exception(self, exc_info: tuple[Any, Any, Any]) -> dict[str, str]:
        exc_type, exc_value, exc_traceback = exc_info
        try:
            rendered = "".join(
                traceback.format_exception(exc_type, exc_value, exc_traceback)
            )
        except Exception:  # noqa: BLE001 - 异常格式化也必须安全失败。
            rendered = _UNSERIALIZABLE
        type_name = getattr(exc_type, "__name__", _UNSERIALIZABLE)
        return {
            "type": _truncate_utf8(str(type_name), self._max_string_bytes),
            "message": _truncate_utf8(
                _safe_exception_message(exc_value), self._max_string_bytes
            ),
            "traceback": _truncate_utf8(rendered, self._max_string_bytes),
        }

    def _is_sensitive_key(self, key: str) -> bool:
        normalized = _normalize_key(key)
        return any(candidate in normalized for candidate in self._sensitive_keys)


def _safe_message(record: logging.LogRecord) -> str:
    try:
        return record.getMessage()
    except Exception:  # noqa: BLE001 - 格式参数错误不能打断业务流程。
        return _UNSERIALIZABLE


def _safe_exception_message(value: BaseException) -> str:
    try:
        return str(value)
    except Exception:  # noqa: BLE001 - 异常对象可能覆盖不安全的 __str__。
        return _UNSERIALIZABLE


def _safe_key(value: object) -> str:
    try:
        return str(value)
    except Exception:  # noqa: BLE001 - 自定义 key 也必须安全失败。
        return _UNSERIALIZABLE


def _normalize_key(key: str) -> str:
    return re.sub(r"[\W_]", "", key, flags=re.UNICODE).lower()


def _truncate_utf8(value: str, limit: int) -> str:
    encoded = value.encode("utf-8")
    if len(encoded) <= limit:
        return value
    prefix = encoded[: limit - len(_TRUNCATED)]
    while prefix:
        try:
            return prefix.decode("utf-8") + _TRUNCATED
        except UnicodeDecodeError:
            prefix = prefix[:-1]
    return _TRUNCATED


def _byte_limit(name: str, value: int) -> int:
    if isinstance(value, bool) or value < len(_TRUNCATED):
        raise ValueError(f"{name} 必须能够容纳截断标记")
    return value


def _positive_limit(name: str, value: int) -> int:
    if isinstance(value, bool) or value < 1:
        raise ValueError(f"{name} 必须是正整数")
    return value
