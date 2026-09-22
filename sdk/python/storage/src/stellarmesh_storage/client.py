"""同步 Storage v1 控制面和预签名数据面客户端。"""

from __future__ import annotations

import os
import tempfile
import time
from pathlib import Path
from types import TracebackType
from typing import TypeVar

import httpx

from . import _operations
from ._common import (
    encode_control_payload,
    ensure_success,
    parse_success,
    response_error,
    retry_delay,
    retryable_status,
    signed_headers,
)
from .constants import MAX_SINGLE_PUT_BYTES, SERVICE_TOKEN_HEADER
from .errors import ClientClosedError, PayloadTooLargeError, UnavailableError
from .models import (
    Checksum,
    ClientConfig,
    CompletedPart,
    MultipartUpload,
    ObjectInfo,
    PresignedRequest,
    path_value,
    request_payload,
)

ModelType = TypeVar("ModelType", bound=ObjectInfo | PresignedRequest | MultipartUpload)


class Client:
    """复用控制面与数据面连接池的同步 Storage 客户端。

    使用 with 管理生命周期，或在应用退出时调用 close()。
    两个池均由客户端创建和关闭；注入 transport 也随所属 HTTPX 池关闭，勿跨
    不同生命周期的客户端共享它。服务 token 只发送到控制面，数据面不跟随重定向。
    关闭后不可再次使用；不要在请求进行时并发关闭。

    Args:
        config: 不可变连接与有限重试配置。
        transport: 可选控制面 HTTPX 传输，用于测试或项目网络配置。
        data_transport: 可选数据面传输，与控制面认证隔离。

    Raises:
        ClientClosedError: 在关闭后发起操作。
        pydantic.ValidationError: 操作输入不满足严格请求模型。
        StorageError: 控制面或数据面请求失败；本地文件错误仍可抛 OSError。
    """

    def __init__(
        self,
        config: ClientConfig,
        *,
        transport: httpx.BaseTransport | None = None,
        data_transport: httpx.BaseTransport | None = None,
    ) -> None:
        self.config = config
        timeout = httpx.Timeout(config.timeout_seconds)
        self._control = httpx.Client(
            base_url=config.base_url,
            headers={SERVICE_TOKEN_HEADER: config.token},
            timeout=timeout,
            transport=transport,
        )
        self._data = httpx.Client(headers={}, timeout=timeout, transport=data_transport)
        self._closed = False

    def __enter__(self) -> Client:
        self._ensure_open()
        return self

    def __exit__(
        self,
        exc_type: type[BaseException] | None,
        exc_value: BaseException | None,
        traceback: TracebackType | None,
    ) -> None:
        self.close()

    def close(self) -> None:
        """关闭控制面和数据面连接池。"""
        if self._closed:
            return
        self._closed = True
        self._control.close()
        self._data.close()

    def stat(
        self, namespace: str, key: str, *, version_id: str | None = None
    ) -> ObjectInfo:
        """读取逻辑对象或指定版本的元数据，不下载对象字节。

        Args:
            namespace: 服务访问文件声明的逻辑空间，不是 Bucket。
            key: 空间内的逻辑对象键。
            version_id: 可选版本，不指定时由对象存储解析当前对象。

        Returns:
            严格校验的 ObjectInfo；ETag 是不透明值，不能假设为 MD5。

        Raises:
            NotFoundError: 对象或版本不存在。
            StorageError: 权限、服务或响应格式错误。可重试故障受 max_attempts 限制。
        """
        return self._model_request(
            _operations.stat(namespace, key, version_id=version_id)
        )

    def delete(
        self, namespace: str, key: str, *, version_id: str | None = None
    ) -> None:
        """删除对象或指定版本；是否产生删除标记由 Bucket 版本策略决定。

        Args:
            namespace: 已授权的逻辑空间。
            key: 逻辑对象键。
            version_id: 显式删除的版本，可省略。

        Raises:
            StorageError: 权限或存储故障；该控制面操作允许有限重试。
        """
        self._empty_request(_operations.delete(namespace, key, version_id=version_id))

    def presign_get(
        self,
        namespace: str,
        key: str,
        *,
        version_id: str | None = None,
        expires_in: int = 900,
    ) -> PresignedRequest:
        """取得下载签名，不发出数据面请求。

        Args:
            namespace: 已授权的逻辑空间。
            key: 逻辑对象键。
            version_id: 可选对象版本。
            expires_in: 有效期秒数，默认 900，协议范围 60 至 3600。

        Returns:
            保留 URL、method、headers 和 expires_at 的签名描述；业务负责保密。

        Raises:
            StorageError: 控制面拒绝、不可用或响应无效。
        """
        return self._model_request(
            _operations.presign_get(
                namespace,
                key,
                version_id=version_id,
                expires_in=expires_in,
            )
        )

    def presign_put(
        self,
        namespace: str,
        key: str,
        *,
        size: int,
        content_type: str | None = None,
        metadata: dict[str, str] | None = None,
        checksum: Checksum | None = None,
        expires_in: int = 900,
    ) -> PresignedRequest:
        """取得单次上传签名，不读取或上传文件。

        Args:
            namespace: 已授权的逻辑空间。
            key: 逻辑对象键。
            size: 声明的字节数，0 至 5 GiB，实际上传必须一致。
            content_type: 可选媒体类型。
            metadata: 用户字符串元数据；条数与字节预算受共享契约限制。
            checksum: 可选标准 Base64 校验和。
            expires_in: 秒数，默认 900，范围 60 至 3600。

        Returns:
            数据面请求描述；不得重写签名 URL 或遗漏签名头。

        Raises:
            StorageError: 控制面拒绝、不可用或响应无效。
        """
        return self._model_request(
            _operations.presign_put(
                namespace,
                key,
                size=size,
                content_type=content_type,
                metadata=metadata,
                checksum=checksum,
                expires_in=expires_in,
            )
        )

    def create_multipart(
        self,
        namespace: str,
        key: str,
        *,
        content_type: str | None = None,
        metadata: dict[str, str] | None = None,
        checksum: Checksum | None = None,
    ) -> MultipartUpload:
        """创建显式分片上传；该操作不自动重试。

        Args:
            namespace: 已授权的逻辑空间。
            key: 逻辑对象键。
            content_type: 可选媒体类型。
            metadata: 可选字符串元数据。
            checksum: 可选 Base64 校验和。

        Returns:
            MultipartUpload；调用方负责保存 upload_id、上传分片并完成或中止。

        Raises:
            StorageError: 请求失败；失败不证明服务端没有创建上传会话。
        """
        return self._model_request(
            _operations.create_multipart(
                namespace,
                key,
                content_type=content_type,
                metadata=metadata,
                checksum=checksum,
            )
        )

    def presign_part(
        self,
        namespace: str,
        key: str,
        upload_id: str,
        part_number: int,
        *,
        expires_in: int = 900,
    ) -> PresignedRequest:
        """取得已有上传会话的单片 PUT 签名，不调度分片上传。

        Args:
            namespace: 逻辑空间。
            key: 原始对象键。
            upload_id: 创建返回的上传标识。
            part_number: 分片编号，1 至 10000。
            expires_in: 有效期秒数，默认 900，范围 60 至 3600。

        Returns:
            必须完整使用的预签名请求。

        Raises:
            StorageError: 权限、上传会话或服务故障。
        """
        return self._model_request(
            _operations.presign_part(
                namespace,
                key,
                upload_id,
                part_number,
                expires_in=expires_in,
            )
        )

    def complete_multipart(
        self,
        namespace: str,
        key: str,
        upload_id: str,
        parts: list[CompletedPart],
    ) -> ObjectInfo:
        """提交分片清单并完成对象，不自动重试。

        Args:
            namespace: 逻辑空间。
            key: 原始对象键。
            upload_id: 原始上传标识。
            parts: 每片编号与上传响应的原始 ETag；编号不可重复。

        Returns:
            完成对象的元数据。

        Raises:
            StorageError: 完成失败或结果不明确；业务决定后续查询或清理。
        """
        return self._model_request(
            _operations.complete_multipart(namespace, key, upload_id, parts)
        )

    def abort_multipart(self, namespace: str, key: str, upload_id: str) -> None:
        """中止指定分片上传；放弃上传时由业务显式调用。

        Args:
            namespace: 逻辑空间。
            key: 原始对象键。
            upload_id: 原始上传标识。

        Raises:
            StorageError: 权限或存储故障；允许有限重试。
        """
        self._empty_request(_operations.abort_multipart(namespace, key, upload_id))

    def upload_bytes(
        self,
        namespace: str,
        key: str,
        data: bytes,
        *,
        content_type: str | None = None,
        metadata: dict[str, str] | None = None,
        checksum: Checksum | None = None,
        expires_in: int = 900,
    ) -> None:
        """先签名，再直接 PUT 字节；不会自动转为 Multipart。

        Args:
            namespace: 逻辑空间。
            key: 逻辑对象键。
            data: 完整字节内容，至多 5 GiB。
            content_type: 可选媒体类型。
            metadata: 可选字符串元数据。
            checksum: 可选 Base64 校验和。
            expires_in: 签名有效期秒数，默认 900。

        Raises:
            PayloadTooLargeError: 超过单次上传限制。
            StorageError: 签名或上传失败。数据面有限重试复用同一签名，不携带服务 token。
        """
        if len(data) > MAX_SINGLE_PUT_BYTES:
            raise PayloadTooLargeError(
                "single PUT exceeds 5 GiB; use the explicit Multipart API"
            )
        presigned = self.presign_put(
            namespace,
            key,
            size=len(data),
            content_type=content_type,
            metadata=metadata,
            checksum=checksum,
            expires_in=expires_in,
        )
        self._data_request(presigned, content=data, retry=True)

    def upload_file(
        self,
        namespace: str,
        key: str,
        source: str | Path,
        *,
        content_type: str | None = None,
        metadata: dict[str, str] | None = None,
        checksum: Checksum | None = None,
        expires_in: int = 900,
    ) -> None:
        """按文件流上传；每次重试重新打开源文件，重试期间不得修改源文件。

        Args:
            namespace: 逻辑空间。
            key: 逻辑对象键。
            source: 本地文件路径；SDK 打开并关闭句柄，不删除源文件。
            content_type: 可选媒体类型。
            metadata: 可选字符串元数据。
            checksum: 可选 Base64 校验和。
            expires_in: 签名有效期秒数，默认 900。

        Raises:
            OSError: 本地文件读取失败。
            PayloadTooLargeError: 超过 5 GiB，需使用显式 Multipart。
            StorageError: 签名或上传失败；重试复用同一签名。
        """
        source_path = path_value(source)
        size = source_path.stat().st_size
        if size > MAX_SINGLE_PUT_BYTES:
            raise PayloadTooLargeError(
                "single PUT exceeds 5 GiB; use the explicit Multipart API"
            )
        presigned = self.presign_put(
            namespace,
            key,
            size=size,
            content_type=content_type,
            metadata=metadata,
            checksum=checksum,
            expires_in=expires_in,
        )
        last_error: Exception | None = None
        for attempt in range(1, self.config.max_attempts + 1):
            with source_path.open("rb") as source_file:
                try:
                    response = self._data.request(
                        presigned.method,
                        presigned.url,
                        headers=signed_headers(presigned.headers),
                        content=source_file,
                    )
                except httpx.TransportError as error:
                    last_error = error
                    if attempt == self.config.max_attempts:
                        break
                else:
                    if response.is_success:
                        return
                    if not retryable_status(response.status_code):
                        raise response_error(response.status_code)
                    last_error = response_error(response.status_code)
                    if attempt == self.config.max_attempts:
                        break
            time.sleep(self._delay(attempt))
        raise UnavailableError("storage data request failed") from last_error

    def download_file(
        self,
        namespace: str,
        key: str,
        target: str | Path,
        *,
        version_id: str | None = None,
        expires_in: int = 900,
    ) -> Path:
        """流式写入同目录临时文件，成功后用 os.replace 覆盖目标。

        Args:
            namespace: 逻辑空间。
            key: 逻辑对象键。
            target: 本地目标路径；自动创建父目录，成功时覆盖已有文件。
            version_id: 可选对象版本。
            expires_in: 签名有效期秒数，默认 900。

        Returns:
            写入后的目标 Path；不会把整个对象缓存在内存中。

        Raises:
            OSError: 本地写入或替换失败。
            StorageError: 签名或下载失败；有限重试使用同一签名与新的临时文件。
        """
        target_path = path_value(target)
        presigned = self.presign_get(
            namespace,
            key,
            version_id=version_id,
            expires_in=expires_in,
        )
        last_error: Exception | None = None
        for attempt in range(1, self.config.max_attempts + 1):
            temporary = self._temporary_path(target_path)
            try:
                with self._data.stream(
                    presigned.method,
                    presigned.url,
                    headers=signed_headers(presigned.headers),
                ) as response:
                    if not response.is_success:
                        if not retryable_status(response.status_code):
                            raise response_error(response.status_code)
                        last_error = response_error(response.status_code)
                    else:
                        with temporary.open("wb") as target_file:
                            for chunk in response.iter_bytes():
                                target_file.write(chunk)
                        os.replace(temporary, target_path)
                        return target_path
            except httpx.TransportError as error:
                last_error = error
            except Exception:
                temporary.unlink(missing_ok=True)
                raise
            temporary.unlink(missing_ok=True)
            if attempt < self.config.max_attempts:
                time.sleep(self._delay(attempt))
        raise UnavailableError("storage data request failed") from last_error

    def _model_request(
        self,
        operation: _operations.ModelOperation[ModelType],
    ) -> ModelType:
        response = self._control_request(
            operation.path,
            request_payload(operation.request),
            retry=operation.retry,
        )
        return parse_success(response, operation.response_model)

    def _empty_request(self, operation: _operations.EmptyOperation) -> None:
        response = self._control_request(
            operation.path,
            request_payload(operation.request),
            retry=operation.retry,
        )
        ensure_success(response)

    def _control_request(
        self, path: str, payload: dict[str, object], *, retry: bool
    ) -> httpx.Response:
        self._ensure_open()
        last_error: Exception | None = None
        attempts = self.config.max_attempts if retry else 1
        encoded = encode_control_payload(payload)
        for attempt in range(1, attempts + 1):
            try:
                response = self._control.post(
                    path,
                    content=encoded,
                    headers={"Content-Type": "application/json"},
                )
            except httpx.TransportError as error:
                last_error = error
            else:
                if response.is_success:
                    return response
                if not retryable_status(response.status_code) or not retry:
                    raise response_error(response.status_code)
                last_error = response_error(response.status_code)
            if attempt < attempts:
                time.sleep(self._delay(attempt))
        raise UnavailableError("storage service request failed") from last_error

    def _data_request(
        self,
        presigned: PresignedRequest,
        *,
        content: bytes,
        retry: bool,
    ) -> None:
        self._ensure_open()
        last_error: Exception | None = None
        attempts = self.config.max_attempts if retry else 1
        for attempt in range(1, attempts + 1):
            try:
                response = self._data.request(
                    presigned.method,
                    presigned.url,
                    headers=signed_headers(presigned.headers),
                    content=content,
                )
            except httpx.TransportError as error:
                last_error = error
            else:
                if response.is_success:
                    return
                if not retryable_status(response.status_code) or not retry:
                    raise response_error(response.status_code)
                last_error = response_error(response.status_code)
            if attempt < attempts:
                time.sleep(self._delay(attempt))
        raise UnavailableError("storage data request failed") from last_error

    def _temporary_path(self, target: Path) -> Path:
        target.parent.mkdir(parents=True, exist_ok=True)
        descriptor, name = tempfile.mkstemp(
            dir=target.parent, prefix=f".{target.name}.", suffix=".tmp"
        )
        os.close(descriptor)
        return Path(name)

    def _delay(self, attempt: int) -> float:
        return retry_delay(
            attempt,
            self.config.initial_backoff_seconds,
            self.config.max_backoff_seconds,
        )

    def _ensure_open(self) -> None:
        if self._closed:
            raise ClientClosedError("storage client is closed")
