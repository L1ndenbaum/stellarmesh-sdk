"""稳定错误类别；底层诊断仅通过 cause 保留，不拼接凭据或签名 URL。"""

from typing import Any

from botocore.exceptions import BotoCoreError, ClientError


class StorageError(Exception):
    """对象存储失败；__cause__ 保留原始异常，日志不应直接展开敏感诊断。"""


class InvalidRequestError(StorageError, ValueError):
    """配置、逻辑 key 或操作参数非法。"""


class NotFoundError(StorageError):
    """对象、版本、Bucket 或分片会话不存在。"""


class ForbiddenError(StorageError):
    """凭据无效或存储端拒绝访问。"""


class ConflictError(StorageError):
    """存储端报告操作冲突。"""


class PreconditionFailedError(StorageError):
    """存储端拒绝条件请求。"""


class UnavailableError(StorageError):
    """连接、超时、服务或无法归类的存储故障。"""


class ClientClosedError(StorageError):
    """客户端未打开或已关闭。"""


def provider_error(error: BotoCoreError | ClientError) -> StorageError:
    kind: type[StorageError] = UnavailableError
    if isinstance(error, ClientError):
        response: dict[str, Any] = error.response
        code = str(response.get("Error", {}).get("Code", ""))
        status = response.get("ResponseMetadata", {}).get("HTTPStatusCode")
        if (
            code
            in {
                "NoSuchKey",
                "NoSuchBucket",
                "NoSuchVersion",
                "NoSuchUpload",
                "NotFound",
            }
            or status == 404
        ):
            kind = NotFoundError
        elif code in {
            "AccessDenied",
            "InvalidAccessKeyId",
            "SignatureDoesNotMatch",
            "ExpiredToken",
        } or status in {401, 403}:
            kind = ForbiddenError
        elif code == "PreconditionFailed" or status == 412:
            kind = PreconditionFailedError
        elif code == "ConditionalRequestConflict" or status == 409:
            kind = ConflictError
        elif status == 400:
            kind = InvalidRequestError
    return kind("对象存储请求失败")
