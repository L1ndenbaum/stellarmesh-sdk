"""复用连接的 aioboto3 异步客户端。"""

import asyncio
from collections.abc import AsyncIterator, Mapping, Sequence
from contextlib import AsyncExitStack, asynccontextmanager
from datetime import UTC, datetime, timedelta
from functools import partial
from pathlib import Path
from types import TracebackType
from typing import Any, cast

import aioboto3
from aiobotocore.config import AioConfig as Config
from botocore.exceptions import BotoCoreError, ClientError

from . import _requests as requests
from ._files import atomic_destination, file_io
from .config import ClientConfig
from .errors import ClientClosedError, InvalidRequestError, provider_error
from .multipart import CompletedPart, MultipartUpload
from .objects import (
    AsyncObjectStream,
    ObjectInfo,
    WriteResult,
    object_info,
    write_result,
)
from .presign import PresignedRequest


class AsyncClient:
    """通过 async with 打开的 aioboto3 客户端；在同一事件循环内复用。

    只关闭自己创建的底层连接，不关闭注入的 Session。必须在生命周期入口打开，
    不在业务调用中懒初始化；关闭前由调用方停止在途请求和对象流。
    """

    def __init__(
        self, config: ClientConfig, *, session: aioboto3.Session | None = None
    ) -> None:
        self.config = config
        self._session = session
        self._stack = AsyncExitStack()
        self._client: Any = None
        self._signer: Any = None
        self._entered = False
        self._closed = False
        self._loop: asyncio.AbstractEventLoop | None = None
        self._close_task: asyncio.Task[None] | None = None

    async def __aenter__(self) -> "AsyncClient":
        if self._entered or self._closed:
            raise ClientClosedError("异步客户端不能重复打开")
        self._entered = True
        self._loop = asyncio.get_running_loop()
        session = self._session or aioboto3.Session()
        options = requests.connection_options(self.config)
        options["config"] = Config(**requests.transport_options(self.config))
        try:
            self._client = await self._stack.enter_async_context(
                session.client("s3", **options)
            )
            self._signer = self._client
            if (
                self.config.presign_endpoint
                and self.config.presign_endpoint != self.config.endpoint
            ):
                options["endpoint_url"] = self.config.presign_endpoint
                self._signer = await self._stack.enter_async_context(
                    session.client("s3", **options)
                )
        except BaseException:
            await self.aclose()
            raise
        return self

    async def __aexit__(
        self,
        exc_type: type[BaseException] | None,
        exc: BaseException | None,
        traceback: TracebackType | None,
    ) -> None:
        await self.aclose()

    async def aclose(self) -> None:
        """关闭底层连接；重复取消也等待清理完成，再传播 CancelledError。"""
        if self._loop is not None and self._loop is not asyncio.get_running_loop():
            raise InvalidRequestError("异步客户端不能跨事件循环关闭")
        self._closed = True
        if self._close_task is None:
            self._close_task = asyncio.create_task(self._stack.aclose())
        canceled: asyncio.CancelledError | None = None
        while not self._close_task.done():
            try:
                await asyncio.shield(self._close_task)
            except asyncio.CancelledError as error:
                canceled = error
            except Exception:
                break
        if canceled is not None:
            if not self._close_task.cancelled():
                self._close_task.exception()
            raise canceled
        self._close_task.result()

    def _ensure_open(self) -> None:
        if self._closed or self._client is None:
            raise ClientClosedError("异步对象存储客户端未打开或已关闭")
        if self._loop is not asyncio.get_running_loop():
            raise InvalidRequestError("异步客户端不能跨事件循环共享")

    async def _call(self, operation: str, params: dict[str, Any]) -> dict[str, Any]:
        self._ensure_open()
        try:
            return cast(
                dict[str, Any], await getattr(self._client, operation)(**params)
            )
        except (BotoCoreError, ClientError) as error:
            raise provider_error(error) from error

    async def check(self) -> None:
        """执行 HeadBucket 检查；不创建 Bucket，也不证明具有全部对象权限。"""
        await self._call("head_bucket", {"Bucket": self.config.bucket})

    async def stat(self, key: str, *, version_id: str | None = None) -> ObjectInfo:
        """读取元数据；version_id 省略时查询当前对象。"""
        return object_info(
            key,
            await self._call(
                "head_object", requests.object_request(self.config, key, version_id)
            ),
        )

    async def delete(self, key: str, *, version_id: str | None = None) -> None:
        """删除对象或指定版本；删除标记行为由 Bucket 版本策略决定。"""
        await self._call(
            "delete_object", requests.object_request(self.config, key, version_id)
        )

    async def upload_bytes(
        self,
        key: str,
        data: bytes,
        *,
        content_type: str | None = None,
        metadata: Mapping[str, str] | None = None,
    ) -> WriteResult:
        """单次上传最多 5 GiB，直接返回写入 ETag，不额外 Stat。"""
        if not isinstance(data, bytes):
            raise InvalidRequestError("data 必须是 bytes")
        params = requests.upload_request(
            self.config, key, len(data), content_type, metadata
        )
        return write_result(await self._call("put_object", {**params, "Body": data}))

    async def upload_file(
        self,
        key: str,
        source: str | Path,
        *,
        content_type: str | None = None,
        metadata: Mapping[str, str] | None = None,
    ) -> WriteResult:
        """单次文件上传，不自动分片；上传及重试期间调用方不得修改源文件。"""
        self._ensure_open()
        with Path(source).open("rb") as body:
            params = requests.upload_request(
                self.config, key, Path(source).stat().st_size, content_type, metadata
            )
            return write_result(
                await self._call("put_object", {**params, "Body": body})
            )

    @asynccontextmanager
    async def open_object(
        self, key: str, *, version_id: str | None = None
    ) -> AsyncIterator[AsyncObjectStream]:
        """在 async with 中读取对象；异常或提前退出都会关闭响应体，读流失败不重放。"""
        response = await self._call(
            "get_object", requests.object_request(self.config, key, version_id)
        )
        body = response["Body"]
        try:
            stream = AsyncObjectStream(object_info(key, response), body)
        except BaseException:
            body.close()
            raise
        try:
            yield stream
        finally:
            await stream.aclose()

    async def download_file(
        self, key: str, destination: str | Path, *, version_id: str | None = None
    ) -> ObjectInfo:
        """下载成功后原子替换目标；失败清理临时文件，父目录须由调用方准备。"""
        async with self.open_object(key, version_id=version_id) as stream:
            with atomic_destination(destination) as (output, _):
                async for chunk in stream.iter_chunks():
                    await file_io(partial(output.write, chunk))
            return stream.info

    async def _presign(
        self,
        operation: str,
        method: str,
        params: dict[str, Any],
        expires_in: int | None,
    ) -> PresignedRequest:
        self._ensure_open()
        ttl = self.config.ttl(expires_in)
        expires_at = datetime.now(UTC) + timedelta(seconds=ttl)
        try:
            url = await self._signer.generate_presigned_url(
                operation, Params=params, ExpiresIn=ttl, HttpMethod=method
            )
        except (BotoCoreError, ClientError) as error:
            raise provider_error(error) from error
        return PresignedRequest(
            url, method, requests.signed_headers(params), expires_at
        )

    async def presign_get(
        self, key: str, *, version_id: str | None = None, expires_in: int | None = None
    ) -> PresignedRequest:
        """签发下载请求；不检查对象存在性，默认有效期 900 秒。"""
        return await self._presign(
            "get_object",
            "GET",
            requests.object_request(self.config, key, version_id),
            expires_in,
        )

    async def presign_put(
        self,
        key: str,
        *,
        size: int,
        content_type: str | None = None,
        metadata: Mapping[str, str] | None = None,
        expires_in: int | None = None,
    ) -> PresignedRequest:
        """签发单次上传；执行时须保留声明的大小、媒体类型及元数据头。"""
        return await self._presign(
            "put_object",
            "PUT",
            requests.upload_request(self.config, key, size, content_type, metadata),
            expires_in,
        )

    async def create_multipart(
        self,
        key: str,
        *,
        content_type: str | None = None,
        metadata: Mapping[str, str] | None = None,
    ) -> MultipartUpload:
        """创建分片会话；调用方负责保存 upload_id 并完成或中止。"""
        params = {
            **requests.object_request(self.config, key),
            **requests.upload_fields(content_type, metadata),
        }
        return MultipartUpload(
            key, (await self._call("create_multipart_upload", params))["UploadId"]
        )

    async def presign_part(
        self,
        key: str,
        upload_id: str,
        part_number: int,
        *,
        expires_in: int | None = None,
    ) -> PresignedRequest:
        """为 1～10000 号分片签发 PUT 请求；签名不证明会话存在。"""
        params = {
            **requests.multipart_request(self.config, key, upload_id),
            "PartNumber": requests.part_number(part_number),
        }
        return await self._presign("upload_part", "PUT", params, expires_in)

    async def complete_multipart(
        self, key: str, upload_id: str, parts: Sequence[CompletedPart]
    ) -> WriteResult:
        """按编号排序后完成上传；拒绝空列表与重复编号，不修改调用方列表。"""
        params = {
            **requests.multipart_request(self.config, key, upload_id),
            "MultipartUpload": requests.completed_parts(parts),
        }
        return write_result(await self._call("complete_multipart_upload", params))

    async def abort_multipart(self, key: str, upload_id: str) -> None:
        """中止分片会话；NoSuchUpload 保持 NotFoundError，不伪装成功。"""
        await self._call(
            "abort_multipart_upload",
            requests.multipart_request(self.config, key, upload_id),
        )
