"""不暴露 token、URL 或 provider 细节的稳定异常。"""


class StorageError(Exception):
    """所有 storage 客户端异常的基类。"""

    def __init__(self, message: str, *, status_code: int | None = None) -> None:
        super().__init__(message)
        self.status_code = status_code


class InvalidRequestError(StorageError):
    """请求参数或结构无效。"""


class PayloadTooLargeError(InvalidRequestError):
    """请求体或上传对象超过限制。"""


class UnauthorizedError(StorageError):
    """storage-service token 无效。"""


class ForbiddenError(StorageError):
    """principal 无 namespace 或 capability 权限。"""


class NotFoundError(StorageError):
    """对象、版本或 Multipart 不存在。"""


class ConflictError(StorageError):
    """对象存储报告冲突。"""


class PreconditionFailedError(StorageError):
    """条件请求未满足。"""


class UnavailableError(StorageError):
    """控制面、数据面或 provider 当前不可用。"""


class ClientClosedError(StorageError):
    """客户端已经关闭。"""
