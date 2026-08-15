"""同步和异步客户端共用的响应、重试与签名头处理。"""

from __future__ import annotations

import json
from typing import TypeVar

import httpx
from pydantic import ValidationError

from .constants import (
    MAX_CONTROL_BODY_BYTES,
    RETRYABLE_STATUS_CODES,
    SERVICE_TOKEN_HEADER,
)
from .errors import (
    ConflictError,
    ForbiddenError,
    InvalidRequestError,
    NotFoundError,
    PayloadTooLargeError,
    PreconditionFailedError,
    StorageError,
    UnauthorizedError,
    UnavailableError,
)
from .models import ApiEnvelope, StrictModel

ModelType = TypeVar("ModelType", bound=StrictModel)


def retry_delay(attempt: int, initial: float, maximum: float) -> float:
    """返回有上限的确定性指数退避。"""
    return float(min(initial * (2 ** (attempt - 1)), maximum))


def retryable_status(status_code: int) -> bool:
    """判断 HTTP 状态是否允许安全操作重试。"""
    return status_code in RETRYABLE_STATUS_CODES


def parse_success(response: httpx.Response, model: type[ModelType]) -> ModelType:
    """严格解析成功 envelope，不在异常中包含原始响应。"""
    try:
        envelope = ApiEnvelope[object].model_validate_json(response.content)
        parsed = model.model_validate_json(json.dumps(envelope.data))
    except (json.JSONDecodeError, ValidationError, ValueError) as error:
        raise UnavailableError(
            "storage service returned an invalid response",
            status_code=response.status_code,
        ) from error
    if envelope.code != response.status_code:
        raise UnavailableError(
            "storage service returned an inconsistent response",
            status_code=response.status_code,
        )
    return parsed


def ensure_success(response: httpx.Response) -> None:
    """校验不需要响应 data 的成功 envelope。"""
    try:
        envelope = ApiEnvelope[dict[str, object]].model_validate_json(response.content)
    except (json.JSONDecodeError, ValidationError, ValueError) as error:
        raise UnavailableError(
            "storage service returned an invalid response",
            status_code=response.status_code,
        ) from error
    if envelope.code != response.status_code:
        raise UnavailableError(
            "storage service returned an inconsistent response",
            status_code=response.status_code,
        )


def response_error(status_code: int) -> StorageError:
    """将服务或数据面状态转换为不含敏感信息的稳定异常。"""
    mapping: dict[int, type[StorageError]] = {
        400: InvalidRequestError,
        401: UnauthorizedError,
        403: ForbiddenError,
        404: NotFoundError,
        409: ConflictError,
        412: PreconditionFailedError,
        413: PayloadTooLargeError,
    }
    error_type = mapping.get(status_code, UnavailableError)
    return error_type("storage request failed", status_code=status_code)


def signed_headers(headers: dict[str, list[str]]) -> list[tuple[str, str]]:
    """保留服务返回的全部 signed header 值。"""
    if any(name.lower() == SERVICE_TOKEN_HEADER.lower() for name in headers):
        raise UnavailableError("storage service returned a forbidden signed header")
    return [(name, value) for name, values in headers.items() for value in values]


def encode_control_payload(payload: dict[str, object]) -> bytes:
    """生成有界 UTF-8 JSON 控制面请求。"""
    encoded = json.dumps(payload, ensure_ascii=False, separators=(",", ":")).encode(
        "utf-8"
    )
    if len(encoded) > MAX_CONTROL_BODY_BYTES:
        raise PayloadTooLargeError("storage control request exceeds 64 KiB")
    return encoded
