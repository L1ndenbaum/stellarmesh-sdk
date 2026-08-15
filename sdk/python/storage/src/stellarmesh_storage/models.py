"""Storage v1 严格请求和响应模型。"""

from __future__ import annotations

import base64
import binascii
import re
from datetime import datetime
from pathlib import Path
from typing import Annotated, Generic, Literal, TypeVar

from pydantic import (
    AnyHttpUrl,
    BaseModel,
    ConfigDict,
    Field,
    StringConstraints,
    field_validator,
    model_validator,
)

from .constants import (
    DEFAULT_PRESIGN_TTL_SECONDS,
    MAX_CONTENT_TYPE_BYTES,
    MAX_METADATA_ITEMS,
    MAX_METADATA_UTF8_BYTES,
    MAX_PART_NUMBER,
    MAX_PHYSICAL_KEY_BYTES,
    MAX_PRESIGN_TTL_SECONDS,
    MAX_SINGLE_PUT_BYTES,
    MIN_PART_NUMBER,
    MIN_PRESIGN_TTL_SECONDS,
    MIN_TOKEN_CHARACTERS,
)

_NAMESPACE_PATTERN = re.compile(r"^[a-z][a-z0-9_-]{0,63}$")
Namespace = Annotated[str, StringConstraints(pattern=r"^[a-z][a-z0-9_-]{0,63}$")]
ChecksumAlgorithm = Literal["CRC32", "CRC32C", "SHA1", "SHA256"]


def _contains_control(value: str) -> bool:
    return any(character.isprintable() is False for character in value)


def _valid_key(value: str) -> str:
    if (
        not value
        or value != value.strip()
        or value.startswith("/")
        or len(value.encode("utf-8")) > MAX_PHYSICAL_KEY_BYTES
        or _contains_control(value)
    ):
        raise ValueError("key 不符合 Storage v1 规则")
    return value


def _valid_optional_text(value: str | None) -> str | None:
    if value is not None and _contains_control(value):
        raise ValueError("字段不能包含控制字符")
    return value


def _valid_required_text(value: str) -> str:
    if not value or value != value.strip() or _contains_control(value):
        raise ValueError("字段不能为空、包含首尾空白或控制字符")
    return value


def _valid_content_type(value: str | None) -> str | None:
    if value is not None and (
        len(value.encode("utf-8")) > MAX_CONTENT_TYPE_BYTES or _contains_control(value)
    ):
        raise ValueError("content_type 超过限制或包含控制字符")
    return value


def _valid_metadata(value: dict[str, str]) -> dict[str, str]:
    if len(value) > MAX_METADATA_ITEMS:
        raise ValueError("metadata 项数超过限制")
    byte_count = 0
    for key, item in value.items():
        if not key or _contains_control(key) or _contains_control(item):
            raise ValueError("metadata key 不能为空或包含控制字符")
        byte_count += len(key.encode("utf-8")) + len(item.encode("utf-8"))
    if byte_count > MAX_METADATA_UTF8_BYTES:
        raise ValueError("metadata 总 UTF-8 字节数超过限制")
    return value


class StrictModel(BaseModel):
    """拒绝未知字段和隐式类型转换的契约模型。"""

    model_config = ConfigDict(extra="forbid", strict=True)


class ClientConfig(StrictModel):
    """同步和异步客户端共享配置。"""

    model_config = ConfigDict(
        extra="forbid", strict=True, frozen=True, hide_input_in_errors=True
    )

    base_url: str
    token: str = Field(repr=False, exclude=True)
    timeout_seconds: float = 5.0
    max_attempts: int = Field(default=3, ge=1, le=10)
    initial_backoff_seconds: float = Field(default=0.1, gt=0, le=60)
    max_backoff_seconds: float = Field(default=1.0, gt=0, le=60)

    @field_validator("base_url")
    @classmethod
    def validate_base_url(cls, value: str) -> str:
        """只接受无凭据、query 和 fragment 的 HTTP(S) 服务根地址。"""
        parsed = AnyHttpUrl(value)
        if parsed.username or parsed.password or parsed.query or parsed.fragment:
            raise ValueError("base_url 不能包含凭据、query 或 fragment")
        return value.rstrip("/")

    @field_validator("token")
    @classmethod
    def validate_token(cls, value: str) -> str:
        if len(value) < MIN_TOKEN_CHARACTERS or _contains_control(value):
            raise ValueError("token 至少需要 32 个字符且不能包含控制字符")
        return value

    @field_validator("timeout_seconds")
    @classmethod
    def validate_timeout(cls, value: float) -> float:
        if value <= 0 or value > 3600:
            raise ValueError("timeout_seconds 必须在 0 到 3600 之间")
        return value

    @model_validator(mode="after")
    def validate_backoff(self) -> ClientConfig:
        if self.initial_backoff_seconds > self.max_backoff_seconds:
            raise ValueError("initial_backoff_seconds 不能大于 max_backoff_seconds")
        return self


class Checksum(StrictModel):
    """S3 规范的 Base64 校验和。"""

    algorithm: ChecksumAlgorithm
    value: str

    @field_validator("value")
    @classmethod
    def validate_value(cls, value: str) -> str:
        try:
            base64.b64decode(value, validate=True)
        except (binascii.Error, ValueError) as error:
            raise ValueError("checksum value 必须使用标准 Base64") from error
        return value


class ObjectRequest(StrictModel):
    namespace: Namespace
    key: str
    version_id: str | None = None

    _validate_key = field_validator("key")(_valid_key)
    _validate_version = field_validator("version_id")(_valid_optional_text)


class PresignGetRequest(ObjectRequest):
    expires_in: int = Field(
        default=DEFAULT_PRESIGN_TTL_SECONDS,
        ge=MIN_PRESIGN_TTL_SECONDS,
        le=MAX_PRESIGN_TTL_SECONDS,
    )


class PresignPutRequest(StrictModel):
    namespace: Namespace
    key: str
    size: int = Field(ge=0, le=MAX_SINGLE_PUT_BYTES)
    content_type: str | None = None
    metadata: dict[str, str] = Field(default_factory=dict)
    checksum: Checksum | None = None
    expires_in: int = Field(
        default=DEFAULT_PRESIGN_TTL_SECONDS,
        ge=MIN_PRESIGN_TTL_SECONDS,
        le=MAX_PRESIGN_TTL_SECONDS,
    )

    _validate_key = field_validator("key")(_valid_key)
    _validate_content_type = field_validator("content_type")(_valid_content_type)
    _validate_metadata = field_validator("metadata")(_valid_metadata)


class MultipartCreateRequest(StrictModel):
    namespace: Namespace
    key: str
    content_type: str | None = None
    metadata: dict[str, str] = Field(default_factory=dict)
    checksum: Checksum | None = None

    _validate_key = field_validator("key")(_valid_key)
    _validate_content_type = field_validator("content_type")(_valid_content_type)
    _validate_metadata = field_validator("metadata")(_valid_metadata)


class MultipartPartRequest(StrictModel):
    namespace: Namespace
    key: str
    upload_id: str
    part_number: int = Field(ge=MIN_PART_NUMBER, le=MAX_PART_NUMBER)
    expires_in: int = Field(
        default=DEFAULT_PRESIGN_TTL_SECONDS,
        ge=MIN_PRESIGN_TTL_SECONDS,
        le=MAX_PRESIGN_TTL_SECONDS,
    )

    _validate_key = field_validator("key")(_valid_key)
    _validate_upload_id = field_validator("upload_id")(_valid_required_text)


class CompletedPart(StrictModel):
    part_number: int = Field(ge=MIN_PART_NUMBER, le=MAX_PART_NUMBER)
    etag: str

    _validate_etag = field_validator("etag")(_valid_required_text)


class MultipartCompleteRequest(StrictModel):
    namespace: Namespace
    key: str
    upload_id: str
    parts: list[CompletedPart] = Field(min_length=1, max_length=MAX_PART_NUMBER)

    _validate_key = field_validator("key")(_valid_key)
    _validate_upload_id = field_validator("upload_id")(_valid_required_text)

    @field_validator("parts")
    @classmethod
    def validate_parts(cls, value: list[CompletedPart]) -> list[CompletedPart]:
        numbers = [part.part_number for part in value]
        if len(numbers) != len(set(numbers)):
            raise ValueError("part_number 不能重复")
        return value


class MultipartAbortRequest(StrictModel):
    namespace: Namespace
    key: str
    upload_id: str

    _validate_key = field_validator("key")(_valid_key)
    _validate_upload_id = field_validator("upload_id")(_valid_required_text)


class ObjectInfo(StrictModel):
    key: str
    version_id: str | None = None
    etag: str | None = None
    size: int = Field(ge=0)
    content_type: str | None = None
    last_modified: datetime | None = None
    metadata: dict[str, str]
    checksum: Checksum | None = None


class PresignedRequest(StrictModel):
    method: Literal["GET", "PUT"]
    url: str
    headers: dict[str, list[str]]
    expires_at: datetime

    @field_validator("url")
    @classmethod
    def validate_url(cls, value: str) -> str:
        parsed = AnyHttpUrl(value)
        if parsed.username or parsed.password:
            raise ValueError("presigned URL 不能包含 userinfo")
        return value


class MultipartUpload(StrictModel):
    key: str
    upload_id: str


ResponseType = TypeVar("ResponseType")


class ApiEnvelope(StrictModel, Generic[ResponseType]):
    code: int
    message: str
    data: ResponseType
    timestamp: datetime
    error_reason: str | None = None


def request_payload(model: StrictModel) -> dict[str, object]:
    """生成不包含 None 的 JSON 请求对象。"""
    return model.model_dump(mode="json", exclude_none=True)


def path_value(path: str | Path) -> Path:
    """将 PathLike 输入统一为 Path。"""
    return Path(path)
