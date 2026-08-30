"""同步与异步客户端共享的 Storage v1 控制面操作描述。"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Generic, TypeVar

from .models import (
    Checksum,
    CompletedPart,
    MultipartAbortRequest,
    MultipartCompleteRequest,
    MultipartCreateRequest,
    MultipartPartRequest,
    MultipartUpload,
    ObjectInfo,
    ObjectRequest,
    PresignedRequest,
    PresignGetRequest,
    PresignPutRequest,
    StrictModel,
)

ResponseType = TypeVar("ResponseType", bound=StrictModel)


@dataclass(frozen=True, slots=True)
class ModelOperation(Generic[ResponseType]):
    """描述返回严格模型的控制面操作。"""

    path: str
    request: StrictModel
    response_model: type[ResponseType]
    retry: bool


@dataclass(frozen=True, slots=True)
class EmptyOperation:
    """描述只校验成功 envelope 的控制面操作。"""

    path: str
    request: StrictModel
    retry: bool


def stat(
    namespace: str, key: str, *, version_id: str | None
) -> ModelOperation[ObjectInfo]:
    return ModelOperation(
        path="/v1/objects/stat",
        request=ObjectRequest(namespace=namespace, key=key, version_id=version_id),
        response_model=ObjectInfo,
        retry=True,
    )


def delete(namespace: str, key: str, *, version_id: str | None) -> EmptyOperation:
    return EmptyOperation(
        path="/v1/objects/delete",
        request=ObjectRequest(namespace=namespace, key=key, version_id=version_id),
        retry=True,
    )


def presign_get(
    namespace: str,
    key: str,
    *,
    version_id: str | None,
    expires_in: int,
) -> ModelOperation[PresignedRequest]:
    return ModelOperation(
        path="/v1/presign/get",
        request=PresignGetRequest(
            namespace=namespace,
            key=key,
            version_id=version_id,
            expires_in=expires_in,
        ),
        response_model=PresignedRequest,
        retry=True,
    )


def presign_put(
    namespace: str,
    key: str,
    *,
    size: int,
    content_type: str | None,
    metadata: dict[str, str] | None,
    checksum: Checksum | None,
    expires_in: int,
) -> ModelOperation[PresignedRequest]:
    return ModelOperation(
        path="/v1/presign/put",
        request=PresignPutRequest(
            namespace=namespace,
            key=key,
            size=size,
            content_type=content_type,
            metadata=metadata or {},
            checksum=checksum,
            expires_in=expires_in,
        ),
        response_model=PresignedRequest,
        retry=True,
    )


def create_multipart(
    namespace: str,
    key: str,
    *,
    content_type: str | None,
    metadata: dict[str, str] | None,
    checksum: Checksum | None,
) -> ModelOperation[MultipartUpload]:
    return ModelOperation(
        path="/v1/multipart/create",
        request=MultipartCreateRequest(
            namespace=namespace,
            key=key,
            content_type=content_type,
            metadata=metadata or {},
            checksum=checksum,
        ),
        response_model=MultipartUpload,
        retry=False,
    )


def presign_part(
    namespace: str,
    key: str,
    upload_id: str,
    part_number: int,
    *,
    expires_in: int,
) -> ModelOperation[PresignedRequest]:
    return ModelOperation(
        path="/v1/multipart/presign-part",
        request=MultipartPartRequest(
            namespace=namespace,
            key=key,
            upload_id=upload_id,
            part_number=part_number,
            expires_in=expires_in,
        ),
        response_model=PresignedRequest,
        retry=True,
    )


def complete_multipart(
    namespace: str,
    key: str,
    upload_id: str,
    parts: list[CompletedPart],
) -> ModelOperation[ObjectInfo]:
    return ModelOperation(
        path="/v1/multipart/complete",
        request=MultipartCompleteRequest(
            namespace=namespace,
            key=key,
            upload_id=upload_id,
            parts=parts,
        ),
        response_model=ObjectInfo,
        retry=False,
    )


def abort_multipart(namespace: str, key: str, upload_id: str) -> EmptyOperation:
    return EmptyOperation(
        path="/v1/multipart/abort",
        request=MultipartAbortRequest(
            namespace=namespace,
            key=key,
            upload_id=upload_id,
        ),
        retry=True,
    )
