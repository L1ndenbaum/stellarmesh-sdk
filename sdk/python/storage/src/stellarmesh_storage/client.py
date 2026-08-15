"""同步 Storage v1 控制面和预签名数据面客户端。"""

from __future__ import annotations

import os
import tempfile
import time
from pathlib import Path
from types import TracebackType
from typing import TypeVar

import httpx

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
    path_value,
    request_payload,
)

ModelType = TypeVar("ModelType", bound=StrictModel)


class Client:
    """同步控制面客户端，并执行显式预签名 GET/PUT。"""

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
        request = ObjectRequest(namespace=namespace, key=key, version_id=version_id)
        return self._model_request("/v1/objects/stat", request, ObjectInfo, retry=True)

    def delete(
        self, namespace: str, key: str, *, version_id: str | None = None
    ) -> None:
        request = ObjectRequest(namespace=namespace, key=key, version_id=version_id)
        self._empty_request("/v1/objects/delete", request, retry=True)

    def presign_get(
        self,
        namespace: str,
        key: str,
        *,
        version_id: str | None = None,
        expires_in: int = 900,
    ) -> PresignedRequest:
        request = PresignGetRequest(
            namespace=namespace,
            key=key,
            version_id=version_id,
            expires_in=expires_in,
        )
        return self._model_request(
            "/v1/presign/get", request, PresignedRequest, retry=True
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
        request = PresignPutRequest(
            namespace=namespace,
            key=key,
            size=size,
            content_type=content_type,
            metadata=metadata or {},
            checksum=checksum,
            expires_in=expires_in,
        )
        return self._model_request(
            "/v1/presign/put", request, PresignedRequest, retry=True
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
        request = MultipartCreateRequest(
            namespace=namespace,
            key=key,
            content_type=content_type,
            metadata=metadata or {},
            checksum=checksum,
        )
        return self._model_request(
            "/v1/multipart/create", request, MultipartUpload, retry=False
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
        request = MultipartPartRequest(
            namespace=namespace,
            key=key,
            upload_id=upload_id,
            part_number=part_number,
            expires_in=expires_in,
        )
        return self._model_request(
            "/v1/multipart/presign-part",
            request,
            PresignedRequest,
            retry=True,
        )

    def complete_multipart(
        self,
        namespace: str,
        key: str,
        upload_id: str,
        parts: list[CompletedPart],
    ) -> ObjectInfo:
        request = MultipartCompleteRequest(
            namespace=namespace, key=key, upload_id=upload_id, parts=parts
        )
        return self._model_request(
            "/v1/multipart/complete", request, ObjectInfo, retry=False
        )

    def abort_multipart(self, namespace: str, key: str, upload_id: str) -> None:
        request = MultipartAbortRequest(
            namespace=namespace, key=key, upload_id=upload_id
        )
        self._empty_request("/v1/multipart/abort", request, retry=True)

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
        path: str,
        request: StrictModel,
        model: type[ModelType],
        *,
        retry: bool,
    ) -> ModelType:
        response = self._control_request(path, request_payload(request), retry=retry)
        return parse_success(response, model)

    def _empty_request(self, path: str, request: StrictModel, *, retry: bool) -> None:
        response = self._control_request(path, request_payload(request), retry=retry)
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
