"""对象操作结果及由上下文持有的读取流。"""

from collections.abc import AsyncIterator, Iterator, Mapping
from dataclasses import dataclass, field
from datetime import datetime
from typing import Any, cast

from botocore.exceptions import BotoCoreError, ClientError

from .errors import ClientClosedError, InvalidRequestError, provider_error

CHUNK_SIZE = 1024 * 1024
MAX_SINGLE_PUT_BYTES = 5 * 1024**3


@dataclass(frozen=True)
class WriteResult:
    """存储端写入结果；ETag 为不透明值，可能缺失，不代表内容 MD5。"""

    etag: str | None
    version_id: str | None = None


@dataclass(frozen=True)
class ObjectInfo:
    """逻辑对象元数据；key 不包含客户端绑定的物理 Prefix。"""

    key: str
    size: int
    etag: str | None = None
    version_id: str | None = None
    content_type: str | None = None
    last_modified: datetime | None = None
    metadata: Mapping[str, str] = field(default_factory=dict)


def object_info(key: str, response: dict[str, Any]) -> ObjectInfo:
    return ObjectInfo(
        key=key,
        size=response["ContentLength"],
        etag=response.get("ETag"),
        version_id=response.get("VersionId"),
        content_type=response.get("ContentType"),
        last_modified=response.get("LastModified"),
        metadata=dict(response.get("Metadata", {})),
    )


def write_result(response: dict[str, Any]) -> WriteResult:
    return WriteResult(response.get("ETag"), response.get("VersionId"))


class ObjectStream:
    """仅在 open_object 上下文内使用的同步流；退出时关闭底层响应。"""

    def __init__(self, info: ObjectInfo, body: Any) -> None:
        self.info = info
        self._body = body
        self._closed = False

    def read(self, size: int = -1) -> bytes:
        """读取至多 size 字节；-1 读取剩余全部，调用方承担内存预算。"""
        if self._closed:
            raise ClientClosedError("对象读取流已关闭")
        if type(size) is not int or size < -1:
            raise InvalidRequestError("读取大小必须是 -1 或非负整数")
        try:
            return cast(bytes, self._body.read(None if size == -1 else size))
        except (BotoCoreError, ClientError) as error:
            raise provider_error(error) from error

    def iter_chunks(self, chunk_size: int = CHUNK_SIZE) -> Iterator[bytes]:
        """按消费进度读取，不预先缓存整个对象。"""
        if type(chunk_size) is not int or chunk_size <= 0:
            raise InvalidRequestError("分块大小必须是正整数")
        while chunk := self.read(chunk_size):
            yield chunk

    def close(self) -> None:
        """幂等关闭响应体；通常由 open_object 上下文自动执行。"""
        if not self._closed:
            self._closed = True
            self._body.close()


class AsyncObjectStream:
    """仅在 open_object 异步上下文内使用的流，不跨事件循环共享。"""

    def __init__(self, info: ObjectInfo, body: Any) -> None:
        self.info = info
        self._body = body
        self._closed = False

    async def read(self, size: int = -1) -> bytes:
        """异步读取；取消保持 CancelledError，不重启已经开始的响应流。"""
        if self._closed:
            raise ClientClosedError("对象读取流已关闭")
        if type(size) is not int or size < -1:
            raise InvalidRequestError("读取大小必须是 -1 或非负整数")
        try:
            return cast(bytes, await self._body.read(None if size == -1 else size))
        except (BotoCoreError, ClientError) as error:
            raise provider_error(error) from error

    async def iter_chunks(self, chunk_size: int = CHUNK_SIZE) -> AsyncIterator[bytes]:
        """按需读取有界分块，消费者控制下一次读取。"""
        if type(chunk_size) is not int or chunk_size <= 0:
            raise InvalidRequestError("分块大小必须是正整数")
        while chunk := await self.read(chunk_size):
            yield chunk

    async def aclose(self) -> None:
        """幂等关闭底层 HTTP 响应；不消费剩余内容。"""
        if not self._closed:
            self._closed = True
            self._body.close()
