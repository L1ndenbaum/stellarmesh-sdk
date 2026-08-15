"""同步客户端控制面、数据面和重试语义测试。"""

from __future__ import annotations

import json
from datetime import UTC, datetime, timedelta
from pathlib import Path

import httpx
import pytest

from stellarmesh_storage import (
    Client,
    ClientClosedError,
    ClientConfig,
    CompletedPart,
    PayloadTooLargeError,
    UnavailableError,
)
from stellarmesh_storage.constants import SERVICE_TOKEN_HEADER

TOKEN = "storage-python-client-token-000000001"


def envelope(data: object, status: int = 200) -> httpx.Response:
    return httpx.Response(
        status,
        json={
            "code": status,
            "message": "操作成功",
            "data": data,
            "timestamp": datetime.now(UTC).isoformat(),
        },
    )


def presigned(method: str = "PUT") -> dict[str, object]:
    return {
        "method": method,
        "url": "https://objects.example/key?X-Amz-Signature=secret",
        "headers": {"X-Signed": ["required"]},
        "expires_at": (datetime.now(UTC) + timedelta(minutes=15)).isoformat(),
    }


def config(*, attempts: int = 3) -> ClientConfig:
    return ClientConfig(
        base_url="http://storage-service:8090",
        token=TOKEN,
        timeout_seconds=5.0,
        max_attempts=attempts,
        initial_backoff_seconds=0.001,
        max_backoff_seconds=0.001,
    )


def test_token_only_goes_to_control_plane_and_signed_headers_are_preserved() -> None:
    control_requests: list[httpx.Request] = []
    data_requests: list[httpx.Request] = []

    def control(request: httpx.Request) -> httpx.Response:
        control_requests.append(request)
        assert request.headers[SERVICE_TOKEN_HEADER] == TOKEN
        assert request.url.path == "/v1/presign/put"
        return envelope(presigned())

    def data(request: httpx.Request) -> httpx.Response:
        data_requests.append(request)
        assert SERVICE_TOKEN_HEADER not in request.headers
        assert request.headers["X-Signed"] == "required"
        assert request.content == b"payload"
        return httpx.Response(200)

    with Client(
        config(),
        transport=httpx.MockTransport(control),
        data_transport=httpx.MockTransport(data),
    ) as client:
        client.upload_bytes("documents", "key", b"payload")

    assert len(control_requests) == 1
    assert len(data_requests) == 1


def test_safe_control_operations_retry_but_create_and_complete_do_not() -> None:
    counts: dict[str, int] = {}

    def handler(request: httpx.Request) -> httpx.Response:
        path = request.url.path
        counts[path] = counts.get(path, 0) + 1
        if path == "/v1/objects/stat" and counts[path] < 3:
            return envelope(None, 503)
        if path == "/v1/objects/stat":
            return envelope({"key": "key", "size": 7, "metadata": {}, "etag": "opaque"})
        return envelope(None, 503)

    client = Client(config(), transport=httpx.MockTransport(handler))
    assert client.stat("documents", "key").size == 7
    assert counts["/v1/objects/stat"] == 3
    with pytest.raises(UnavailableError):
        client.create_multipart("documents", "key")
    assert counts["/v1/multipart/create"] == 1
    with pytest.raises(UnavailableError):
        client.complete_multipart(
            "documents", "key", "upload", [CompletedPart(part_number=1, etag="e")]
        )
    assert counts["/v1/multipart/complete"] == 1
    client.close()


def test_remaining_control_methods_use_fixed_routes() -> None:
    paths: list[str] = []

    def handler(request: httpx.Request) -> httpx.Response:
        paths.append(request.url.path)
        if request.url.path in {"/v1/presign/get", "/v1/multipart/presign-part"}:
            return envelope(presigned("GET"))
        return envelope({})

    client = Client(config(), transport=httpx.MockTransport(handler))
    client.delete("documents", "key", version_id="v1")
    assert client.presign_get("documents", "key").method == "GET"
    assert client.presign_part("documents", "key", "upload", 1).method == "GET"
    client.abort_multipart("documents", "key", "upload")
    assert paths == [
        "/v1/objects/delete",
        "/v1/presign/get",
        "/v1/multipart/presign-part",
        "/v1/multipart/abort",
    ]
    client.close()


def test_oversized_control_payload_is_rejected_before_http() -> None:
    called = False

    def handler(_: httpx.Request) -> httpx.Response:
        nonlocal called
        called = True
        return envelope({})

    client = Client(config(), transport=httpx.MockTransport(handler))
    parts = [
        CompletedPart(part_number=number, etag="x" * 20) for number in range(1, 10_001)
    ]
    with pytest.raises(PayloadTooLargeError):
        client.complete_multipart("documents", "key", "upload", parts)
    assert called is False
    client.close()


def test_download_is_atomic_and_cleans_temporary_file(tmp_path: Path) -> None:
    target = tmp_path / "object.bin"
    target.write_bytes(b"old")

    def control(request: httpx.Request) -> httpx.Response:
        assert request.url.path == "/v1/presign/get"
        return envelope(presigned("GET"))

    def failing_data(request: httpx.Request) -> httpx.Response:
        assert SERVICE_TOKEN_HEADER not in request.headers
        raise httpx.ReadError("URL https://objects.example/?secret", request=request)

    client = Client(
        config(attempts=1),
        transport=httpx.MockTransport(control),
        data_transport=httpx.MockTransport(failing_data),
    )
    with pytest.raises(UnavailableError) as captured:
        client.download_file("documents", "key", target)
    assert "objects.example" not in str(captured.value)
    assert target.read_bytes() == b"old"
    assert list(tmp_path.glob(".object.bin.*.tmp")) == []
    client.close()

    successful = Client(
        config(),
        transport=httpx.MockTransport(control),
        data_transport=httpx.MockTransport(
            lambda _: httpx.Response(200, content=b"new")
        ),
    )
    assert successful.download_file("documents", "key", target) == target
    assert target.read_bytes() == b"new"
    successful.close()


def test_upload_file_reopens_on_retry(tmp_path: Path) -> None:
    source = tmp_path / "source.bin"
    source.write_bytes(b"complete-payload")
    uploads: list[bytes] = []

    def control(_: httpx.Request) -> httpx.Response:
        return envelope(presigned())

    def data(request: httpx.Request) -> httpx.Response:
        uploads.append(request.read())
        return httpx.Response(503 if len(uploads) == 1 else 200)

    client = Client(
        config(attempts=2),
        transport=httpx.MockTransport(control),
        data_transport=httpx.MockTransport(data),
    )
    client.upload_file("documents", "key", source)
    assert uploads == [b"complete-payload", b"complete-payload"]
    client.close()


def test_errors_and_closed_state_never_include_secrets() -> None:
    def handler(_: httpx.Request) -> httpx.Response:
        return httpx.Response(
            503,
            content=json.dumps(
                {"url": "https://private/?X-Amz-Signature=secret", "token": TOKEN}
            ).encode(),
        )

    client = Client(config(attempts=1), transport=httpx.MockTransport(handler))
    with pytest.raises(UnavailableError) as captured:
        client.stat("documents", "key")
    message = str(captured.value)
    assert "private" not in message
    assert "Signature" not in message
    assert TOKEN not in message
    client.close()
    with pytest.raises(ClientClosedError):
        client.stat("documents", "key")


def test_service_token_header_is_rejected_from_presigned_response() -> None:
    malicious = presigned()
    malicious["headers"] = {SERVICE_TOKEN_HEADER: [TOKEN]}

    client = Client(
        config(),
        transport=httpx.MockTransport(lambda _: envelope(malicious)),
        data_transport=httpx.MockTransport(lambda _: httpx.Response(200)),
    )
    with pytest.raises(UnavailableError) as captured:
        client.upload_bytes("documents", "key", b"payload")
    assert TOKEN not in str(captured.value)
    client.close()
