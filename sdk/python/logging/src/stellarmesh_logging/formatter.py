"""标准库 logging 的安全单行 JSON Formatter。"""

from __future__ import annotations

import json
import logging
import math
import traceback
from collections.abc import Collection, Mapping
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
    "accesstoken",
    "refreshtoken",
    "idtoken",
    "sessiontoken",
    "csrftoken",
    "xsrftoken",
    "setcookie",
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
        self._sensitive_keys = frozenset(sensitive_keys)

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
        # Enum 是显式值转换；其余容器仅接受内置类型，不调用业务迭代器。
        if isinstance(value, Enum):
            return self._sanitize(value.value, depth, budget, seen)
        if type(value) is str:
            return _truncate_utf8(value, self._max_string_bytes)
        if isinstance(value, BaseException):
            return _truncate_utf8(
                _safe_exception_message(value), self._max_string_bytes
            )
        if type(value) is bytes:
            return f"<bytes:{len(value)}>"
        if value is None or type(value) in (bool, int):
            return value
        if type(value) is float:
            return value if math.isfinite(value) else _UNSERIALIZABLE
        if type(value) in (datetime, date, UUID):
            return _truncate_utf8(str(value), self._max_string_bytes)
        if type(value) not in (dict, list, tuple):
            return _UNSERIALIZABLE
        if id(value) in seen:
            return _UNSERIALIZABLE
        seen.add(id(value))
        try:
            if isinstance(value, dict):
                result: dict[str, object] = {}
                for key, nested in value.items():
                    if not budget.consume():
                        break
                    if type(key) is not str:
                        return _UNSERIALIZABLE
                    result[key] = self._sanitize_keyed(
                        key, nested, depth + 1, budget, seen
                    )
                return result
            sequence: list[object] = []
            # 上面的精确类型检查排除了自定义 Sequence 和容器子类。
            assert isinstance(value, (list, tuple))
            for nested in value:
                if not budget.consume():
                    break
                sequence.append(self._sanitize(nested, depth + 1, budget, seen))
            return sequence
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
        return _normalize_key(key) in self._sensitive_keys


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


def _normalize_key(key: str) -> str:
    return "".join(
        character.lower()
        for character in key
        if character.isalpha() or character.isdecimal()
    )


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
    if type(value) is not int or value < len(_TRUNCATED):
        raise ValueError(f"{name} 必须能够容纳截断标记")
    return value


def _positive_limit(name: str, value: int) -> int:
    if type(value) is not int or value < 1:
        raise ValueError(f"{name} 必须是正整数")
    return value
