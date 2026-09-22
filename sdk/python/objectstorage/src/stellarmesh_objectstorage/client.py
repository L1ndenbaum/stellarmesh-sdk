"""复用连接的 Boto3 同步客户端。"""

from collections.abc import Iterator, Mapping, Sequence
from contextlib import ExitStack, contextmanager
from datetime import UTC, datetime, timedelta
from pathlib import Path
from types import TracebackType
from typing import Any, cast

import boto3
from botocore.config import Config
from botocore.exceptions import BotoCoreError, ClientError

from . import _requests as requests
from ._files import atomic_destination
from .config import ClientConfig
from .errors import ClientClosedError, InvalidRequestError, provider_error
from .multipart import CompletedPart, MultipartUpload
from .objects import ObjectInfo, ObjectStream, WriteResult, object_info, write_result
from .presign import PresignedRequest


class Client:
    """固定绑定 Bucket／Prefix 的同步对象存储客户端。

    Session 可注入，否则使用标准 AWS 凭据链。客户端拥有其创建的连接，
    调用方使用 with 或 close 关闭；不要跨进程共享或在请求中途并发关闭。
    不创建 Bucket，不执行隐式健康检查，不管理业务上传会话。
    """

    def __init__(
        self, config: ClientConfig, *, session: boto3.Session | None = None
    ) -> None:
        self.config = config
        self._stack = ExitStack()
        self._closed = False
        session = session or boto3.Session()
        options = requests.connection_options(config)
        options["config"] = Config(**requests.transport_options(config))
        try:
            self._client = session.client("s3", **options)
            self._stack.callback(self._client.close)
            self._signer = self._client
            if config.presign_endpoint and config.presign_endpoint != config.endpoint:
                options["endpoint_url"] = config.presign_endpoint
                self._signer = session.client("s3", **options)
                self._stack.callback(self._signer.close)
        except BaseException:
            self._stack.close()
            raise

    def __enter__(self) -> "Client":
        self._ensure_open()
        return self

    def __exit__(
        self,
        exc_type: type[BaseException] | None,
        exc: BaseException | None,
        traceback: TracebackType | None,
    ) -> None:
        self.close()

    def close(self) -> None:
        """幂等关闭创建的底层客户端；不关闭业务注入的 Session。"""
        if not self._closed:
            self._closed = True
            self._stack.close()

    def _ensure_open(self) -> None:
        if self._closed:
            raise ClientClosedError("对象存储客户端已关闭")

    def _call(self, operation: str, params: dict[str, Any]) -> dict[str, Any]:
        self._ensure_open()
        try:
            return cast(dict[str, Any], getattr(self._client, operation)(**params))
        except (BotoCoreError, ClientError) as error:
            raise provider_error(error) from error

    def check(self) -> None:
        """执行 HeadBucket 检查；不创建 Bucket，也不证明具有全部对象权限。"""
        self._call("head_bucket", {"Bucket": self.config.bucket})

    def stat(self, key: str, *, version_id: str | None = None) -> ObjectInfo:
        """读取元数据；version_id 省略时查询当前对象。"""
        return object_info(
            key,
            self._call(
                "head_object", requests.object_request(self.config, key, version_id)
            ),
        )

    def delete(self, key: str, *, version_id: str | None = None) -> None:
        """删除对象或指定版本；删除标记行为由 Bucket 版本策略决定。"""
        self._call(
            "delete_object", requests.object_request(self.config, key, version_id)
        )

    def upload_bytes(
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
        return write_result(self._call("put_object", {**params, "Body": data}))

    def upload_file(
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
            return write_result(self._call("put_object", {**params, "Body": body}))

    @contextmanager
    def open_object(
        self, key: str, *, version_id: str | None = None
    ) -> Iterator[ObjectStream]:
        """在 with 中读取对象；异常或提前退出都会关闭响应体，读流失败不重放。"""
        response = self._call(
            "get_object", requests.object_request(self.config, key, version_id)
        )
        body = response["Body"]
        try:
            stream = ObjectStream(object_info(key, response), body)
        except BaseException:
            body.close()
            raise
        try:
            yield stream
        finally:
            stream.close()

    def download_file(
        self, key: str, destination: str | Path, *, version_id: str | None = None
    ) -> ObjectInfo:
        """下载成功后原子替换目标；失败清理临时文件，父目录须由调用方准备。"""
        with self.open_object(key, version_id=version_id) as stream:
            with atomic_destination(destination) as (output, _):
                for chunk in stream.iter_chunks():
                    output.write(chunk)
            return stream.info

    def _presign(
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
            url = self._signer.generate_presigned_url(
                operation, Params=params, ExpiresIn=ttl, HttpMethod=method
            )
        except (BotoCoreError, ClientError) as error:
            raise provider_error(error) from error
        return PresignedRequest(
            url, method, requests.signed_headers(params), expires_at
        )

    def presign_get(
        self, key: str, *, version_id: str | None = None, expires_in: int | None = None
    ) -> PresignedRequest:
        """签发下载请求；不检查对象存在性，默认有效期 900 秒。"""
        return self._presign(
            "get_object",
            "GET",
            requests.object_request(self.config, key, version_id),
            expires_in,
        )

    def presign_put(
        self,
        key: str,
        *,
        size: int,
        content_type: str | None = None,
        metadata: Mapping[str, str] | None = None,
        expires_in: int | None = None,
    ) -> PresignedRequest:
        """签发单次上传；执行时须保留声明的大小、媒体类型及元数据头。"""
        return self._presign(
            "put_object",
            "PUT",
            requests.upload_request(self.config, key, size, content_type, metadata),
            expires_in,
        )

    def create_multipart(
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
            key, self._call("create_multipart_upload", params)["UploadId"]
        )

    def presign_part(
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
        return self._presign("upload_part", "PUT", params, expires_in)

    def complete_multipart(
        self, key: str, upload_id: str, parts: Sequence[CompletedPart]
    ) -> WriteResult:
        """按编号排序后完成上传；拒绝空列表与重复编号，不修改调用方列表。"""
        params = {
            **requests.multipart_request(self.config, key, upload_id),
            "MultipartUpload": requests.completed_parts(parts),
        }
        return write_result(self._call("complete_multipart_upload", params))

    def abort_multipart(self, key: str, upload_id: str) -> None:
        """中止分片会话；NoSuchUpload 保持 NotFoundError，不伪装成功。"""
        self._call(
            "abort_multipart_upload",
            requests.multipart_request(self.config, key, upload_id),
        )
