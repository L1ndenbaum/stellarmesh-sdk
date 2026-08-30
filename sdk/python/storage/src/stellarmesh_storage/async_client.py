"""异步 Storage v1 控制面和预签名数据面客户端。"""

from __future__ import annotations

import asyncio
import os
import tempfile
from collections.abc import AsyncIterator
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


class AsyncClient:
    """异步控制面客户端，并执行显式预签名 GET/PUT。"""

    def __init__(
        self,
        config: ClientConfig,
        *,
        transport: httpx.AsyncBaseTransport | None = None,
        data_transport: httpx.AsyncBaseTransport | None = None,
    ) -> None:
        self.config = config
        timeout = httpx.Timeout(config.timeout_seconds)
        self._control = httpx.AsyncClient(
            base_url=config.base_url,
            headers={SERVICE_TOKEN_HEADER: config.token},
            timeout=timeout,
            transport=transport,
        )
        self._data = httpx.AsyncClient(
            headers={}, timeout=timeout, transport=data_transport
        )
        self._closed = False

    async def __aenter__(self) -> AsyncClient:
        self._ensure_open()
        return self

    async def __aexit__(
        self,
        exc_type: type[BaseException] | None,
        exc_value: BaseException | None,
        traceback: TracebackType | None,
    ) -> None:
        await self.aclose()

    async def aclose(self) -> None:
        """关闭控制面和数据面连接池。"""
        if self._closed:
            return
        self._closed = True
        await self._control.aclose()
        await self._data.aclose()

    async def stat(
        self, namespace: str, key: str, *, version_id: str | None = None
    ) -> ObjectInfo:
        return await self._model_request(
            _operations.stat(namespace, key, version_id=version_id)
        )

    async def delete(
        self, namespace: str, key: str, *, version_id: str | None = None
    ) -> None:
        await self._empty_request(
            _operations.delete(namespace, key, version_id=version_id)
        )

    async def presign_get(
        self,
        namespace: str,
        key: str,
        *,
        version_id: str | None = None,
        expires_in: int = 900,
    ) -> PresignedRequest:
        return await self._model_request(
            _operations.presign_get(
                namespace,
                key,
                version_id=version_id,
                expires_in=expires_in,
            )
        )

    async def presign_put(
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
        return await self._model_request(
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

    async def create_multipart(
        self,
        namespace: str,
        key: str,
        *,
        content_type: str | None = None,
        metadata: dict[str, str] | None = None,
        checksum: Checksum | None = None,
    ) -> MultipartUpload:
        return await self._model_request(
            _operations.create_multipart(
                namespace,
                key,
                content_type=content_type,
                metadata=metadata,
                checksum=checksum,
            )
        )

    async def presign_part(
        self,
        namespace: str,
        key: str,
        upload_id: str,
        part_number: int,
        *,
        expires_in: int = 900,
    ) -> PresignedRequest:
        return await self._model_request(
            _operations.presign_part(
                namespace,
                key,
                upload_id,
                part_number,
                expires_in=expires_in,
            )
        )

    async def complete_multipart(
        self,
        namespace: str,
        key: str,
        upload_id: str,
        parts: list[CompletedPart],
    ) -> ObjectInfo:
        return await self._model_request(
            _operations.complete_multipart(namespace, key, upload_id, parts)
        )

    async def abort_multipart(self, namespace: str, key: str, upload_id: str) -> None:
        await self._empty_request(
            _operations.abort_multipart(namespace, key, upload_id)
        )

    async def upload_bytes(
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
        if len(data) > MAX_SINGLE_PUT_BYTES:
            raise PayloadTooLargeError(
                "single PUT exceeds 5 GiB; use the explicit Multipart API"
            )
        presigned = await self.presign_put(
            namespace,
            key,
            size=len(data),
            content_type=content_type,
            metadata=metadata,
            checksum=checksum,
            expires_in=expires_in,
        )
        await self._data_request(presigned, content=data, retry=True)

    async def upload_file(
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
        source_path = path_value(source)
        size = source_path.stat().st_size
        if size > MAX_SINGLE_PUT_BYTES:
            raise PayloadTooLargeError(
                "single PUT exceeds 5 GiB; use the explicit Multipart API"
            )
        presigned = await self.presign_put(
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
            try:
                response = await self._data.request(
                    presigned.method,
                    presigned.url,
                    headers=signed_headers(presigned.headers),
                    content=self._file_chunks(source_path),
                )
            except httpx.TransportError as error:
                last_error = error
            else:
                if response.is_success:
                    return
                if not retryable_status(response.status_code):
                    raise response_error(response.status_code)
                last_error = response_error(response.status_code)
            if attempt < self.config.max_attempts:
                await asyncio.sleep(self._delay(attempt))
        raise UnavailableError("storage data request failed") from last_error

    async def download_file(
        self,
        namespace: str,
        key: str,
        target: str | Path,
        *,
        version_id: str | None = None,
        expires_in: int = 900,
    ) -> Path:
        target_path = path_value(target)
        presigned = await self.presign_get(
            namespace,
            key,
            version_id=version_id,
            expires_in=expires_in,
        )
        last_error: Exception | None = None
        for attempt in range(1, self.config.max_attempts + 1):
            temporary = self._temporary_path(target_path)
            try:
                async with self._data.stream(
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
                            async for chunk in response.aiter_bytes():
                                await asyncio.to_thread(target_file.write, chunk)
                        os.replace(temporary, target_path)
                        return target_path
            except httpx.TransportError as error:
                last_error = error
            except Exception:
                temporary.unlink(missing_ok=True)
                raise
            temporary.unlink(missing_ok=True)
            if attempt < self.config.max_attempts:
                await asyncio.sleep(self._delay(attempt))
        raise UnavailableError("storage data request failed") from last_error

    async def _model_request(
        self,
        operation: _operations.ModelOperation[ModelType],
    ) -> ModelType:
        response = await self._control_request(
            operation.path,
            request_payload(operation.request),
            retry=operation.retry,
        )
        return parse_success(response, operation.response_model)

    async def _empty_request(self, operation: _operations.EmptyOperation) -> None:
        response = await self._control_request(
            operation.path,
            request_payload(operation.request),
            retry=operation.retry,
        )
        ensure_success(response)

    async def _control_request(
        self, path: str, payload: dict[str, object], *, retry: bool
    ) -> httpx.Response:
        self._ensure_open()
        last_error: Exception | None = None
        attempts = self.config.max_attempts if retry else 1
        encoded = encode_control_payload(payload)
        for attempt in range(1, attempts + 1):
            try:
                response = await self._control.post(
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
                await asyncio.sleep(self._delay(attempt))
        raise UnavailableError("storage service request failed") from last_error

    async def _data_request(
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
                response = await self._data.request(
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
                await asyncio.sleep(self._delay(attempt))
        raise UnavailableError("storage data request failed") from last_error

    async def _file_chunks(self, path: Path) -> AsyncIterator[bytes]:
        with path.open("rb") as source_file:
            while chunk := await asyncio.to_thread(source_file.read, 1024 * 1024):
                yield chunk

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
