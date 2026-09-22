"""Python 进程内 S3／MinIO 客户端；业务配置、权限与资源部署由调用方负责。"""

from .async_client import AsyncClient
from .client import Client
from .config import ClientConfig
from .errors import (
    ClientClosedError,
    ConflictError,
    ForbiddenError,
    InvalidRequestError,
    NotFoundError,
    PreconditionFailedError,
    StorageError,
    UnavailableError,
)
from .multipart import CompletedPart, MultipartUpload
from .objects import AsyncObjectStream, ObjectInfo, ObjectStream, WriteResult
from .presign import PresignedRequest

__all__ = [
    "AsyncClient",
    "AsyncObjectStream",
    "Client",
    "ClientClosedError",
    "ClientConfig",
    "CompletedPart",
    "ConflictError",
    "ForbiddenError",
    "InvalidRequestError",
    "MultipartUpload",
    "NotFoundError",
    "ObjectInfo",
    "ObjectStream",
    "PreconditionFailedError",
    "PresignedRequest",
    "StorageError",
    "UnavailableError",
    "WriteResult",
]
