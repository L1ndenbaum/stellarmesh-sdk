"""同步与异步客户端共享纯参数准备；不持有网络或刷新状态。"""

from collections.abc import Mapping, Sequence
from typing import Any

from .config import ClientConfig, text
from .errors import InvalidRequestError
from .multipart import CompletedPart
from .objects import MAX_SINGLE_PUT_BYTES


def connection_options(config: ClientConfig) -> dict[str, Any]:
    return {
        "region_name": config.region,
        "endpoint_url": config.endpoint,
    }


def transport_options(config: ClientConfig) -> dict[str, Any]:
    return {
        "signature_version": "s3v4",
        "connect_timeout": config.connect_timeout,
        "read_timeout": config.read_timeout,
        "retries": {"mode": "standard", "total_max_attempts": config.max_attempts},
        "s3": {"addressing_style": "path" if config.use_path_style else "virtual"},
        # 使用各厂商都支持的必要校验；不隐式为普通上传启用 aws-chunked。
        "request_checksum_calculation": "when_required",
        "response_checksum_validation": "when_required",
    }


def object_request(
    config: ClientConfig, key: str, version_id: str | None = None
) -> dict[str, Any]:
    result: dict[str, Any] = {"Bucket": config.bucket, "Key": config.physical_key(key)}
    if version_id is not None:
        result["VersionId"] = text(version_id, "version_id")
    return result


def upload_fields(
    content_type: str | None, metadata: Mapping[str, str] | None
) -> dict[str, Any]:
    result: dict[str, Any] = {}
    if content_type is not None:
        result["ContentType"] = text(content_type, "content_type")
    if metadata is not None:
        copied = {}
        for key, value in metadata.items():
            text(key, "metadata key")
            text(value, "metadata value", empty=True)
            # HTTP 头字段名大小写不敏感，避免签名与实际传输出现两个来源。
            lowered = key.lower()
            if lowered in copied:
                raise InvalidRequestError("metadata 字段名不能大小写重复")
            try:
                key.encode("ascii")
                value.encode("ascii")
            except UnicodeError as error:
                raise InvalidRequestError("S3 metadata 必须使用 ASCII") from error
            if any(
                char not in "!#$%&'*+-.^_`|~0123456789abcdefghijklmnopqrstuvwxyz"
                for char in lowered
            ):
                raise InvalidRequestError("metadata 字段名不是合法 HTTP token")
            copied[lowered] = value
        result["Metadata"] = copied
    return result


def size_value(size: int) -> int:
    if type(size) is not int or not 0 <= size <= MAX_SINGLE_PUT_BYTES:
        raise InvalidRequestError("单次上传大小须在 0～5 GiB；较大对象请使用 Multipart")
    return size


def upload_request(
    config: ClientConfig,
    key: str,
    size: int,
    content_type: str | None,
    metadata: Mapping[str, str] | None,
) -> dict[str, Any]:
    return {
        **object_request(config, key),
        "ContentLength": size_value(size),
        **upload_fields(content_type, metadata),
    }


def multipart_request(config: ClientConfig, key: str, upload_id: str) -> dict[str, Any]:
    return {**object_request(config, key), "UploadId": text(upload_id, "upload_id")}


def part_number(value: int) -> int:
    if type(value) is not int or not 1 <= value <= 10000:
        raise InvalidRequestError("分片编号必须在 1～10000 之间")
    return value


def completed_parts(parts: Sequence[CompletedPart]) -> dict[str, Any]:
    if not parts:
        raise InvalidRequestError("完成分片列表不能为空")
    seen: set[int] = set()
    result = []
    for part in parts:
        number = part_number(part.part_number)
        if number in seen:
            raise InvalidRequestError("分片编号不能重复")
        seen.add(number)
        etag = text(part.etag, "etag")
        if not etag.strip():
            raise InvalidRequestError("etag 不能为空白")
        result.append({"PartNumber": number, "ETag": etag})
    return {"Parts": sorted(result, key=lambda part: part["PartNumber"])}


def signed_headers(params: dict[str, Any]) -> dict[str, str]:
    headers = (
        {"Content-Length": str(params["ContentLength"])}
        if "ContentLength" in params
        else {}
    )
    if "ContentType" in params:
        headers["Content-Type"] = params["ContentType"]
    for key, value in params.get("Metadata", {}).items():
        headers["x-amz-meta-" + key] = value
    return headers
