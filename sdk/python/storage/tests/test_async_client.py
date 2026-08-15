"""异步客户端与同步客户端的安全和重试一致性测试。"""

from __future__ import annotations

from datetime import UTC, datetime, timedelta
from pathlib import Path

import httpx
import pytest

from stellarmesh_storage import (
    AsyncClient,
    ClientClosedError,
    ClientConfig,
    UnavailableError,
)
from stellarmesh_storage.constants import SERVICE_TOKEN_HEADER

TOKEN = "storage-python-async-token-0000000001"


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


def config() -> ClientConfig:
    return ClientConfig(
        base_url="http://storage-service:8090",
        token=TOKEN,
        timeout_seconds=5.0,
        max_attempts=2,
        initial_backoff_seconds=0.001,
        max_backoff_seconds=0.001,
    )


@pytest.mark.asyncio
async def test_async_upload_never_forwards_service_token() -> None:
    control_count = 0
    data_count = 0

    async def control(request: httpx.Request) -> httpx.Response:
        nonlocal control_count
        control_count += 1
        assert request.headers[SERVICE_TOKEN_HEADER] == TOKEN
        return envelope(
            {
                "method": "PUT",
                "url": "https://objects.example/key?signature=secret",
                "headers": {"X-Signed": ["required"]},
                "expires_at": (datetime.now(UTC) + timedelta(minutes=15)).isoformat(),
            }
        )

    async def data(request: httpx.Request) -> httpx.Response:
        nonlocal data_count
        data_count += 1
        assert SERVICE_TOKEN_HEADER not in request.headers
        assert request.headers["X-Signed"] == "required"
        assert await request.aread() == b"payload"
        return httpx.Response(200)

    async with AsyncClient(
        config(),
        transport=httpx.MockTransport(control),
        data_transport=httpx.MockTransport(data),
    ) as client:
        await client.upload_bytes("documents", "key", b"payload")
    assert control_count == 1
    assert data_count == 1


@pytest.mark.asyncio
async def test_async_download_is_atomic(tmp_path: Path) -> None:
    target = tmp_path / "target.bin"
    target.write_bytes(b"old")

    async def control(_: httpx.Request) -> httpx.Response:
        return envelope(
            {
                "method": "GET",
                "url": "https://objects.example/key?signature=secret",
                "headers": {},
                "expires_at": (datetime.now(UTC) + timedelta(minutes=15)).isoformat(),
            }
        )

    async def data(_: httpx.Request) -> httpx.Response:
        return httpx.Response(200, content=b"new")

    client = AsyncClient(
        config(),
        transport=httpx.MockTransport(control),
        data_transport=httpx.MockTransport(data),
    )
    assert await client.download_file("documents", "key", target) == target
    assert target.read_bytes() == b"new"
    await client.aclose()
    with pytest.raises(ClientClosedError):
        await client.stat("documents", "key")


@pytest.mark.asyncio
async def test_async_create_does_not_retry_uncertain_result() -> None:
    count = 0

    async def control(_: httpx.Request) -> httpx.Response:
        nonlocal count
        count += 1
        return envelope(None, 503)

    client = AsyncClient(config(), transport=httpx.MockTransport(control))
    with pytest.raises(UnavailableError):
        await client.create_multipart("documents", "key")
    assert count == 1
    await client.aclose()
