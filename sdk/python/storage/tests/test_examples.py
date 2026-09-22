"""文档源码使用公开入口，测试替身分别模拟控制面与数据面。"""

import inspect
from pathlib import Path

import httpx
import pytest
from example_storage import download_example, upload_example

from stellarmesh_storage import AsyncClient, Client, ClientConfig

TOKEN = "documentation-example-token-000000001"


class ExampleTransport(httpx.MockTransport):
    """记录公开关闭动作，不依赖客户端私有连接池字段。"""

    closed = False

    def close(self) -> None:
        self.closed = True
        super().close()

    async def aclose(self) -> None:
        self.closed = True
        await super().aclose()


def control(request: httpx.Request) -> httpx.Response:
    assert request.headers["X-Storage-Service-Token"] == TOKEN
    if request.url.path == "/v1/objects/stat":
        data: object = {"key": "example.txt", "size": 7, "metadata": {}}
    else:
        method = "GET" if request.url.path.endswith("get") else "PUT"
        data = {
            "method": method,
            "url": "https://objects.example/example.txt?signature=example",
            "headers": {"X-Signed": ["required"]},
            "expires_at": "2030-01-01T00:00:00Z",
        }
    return httpx.Response(
        200,
        json={
            "code": 200,
            "message": "成功",
            "data": data,
            "timestamp": "2026-01-01T00:00:00Z",
        },
    )


def data(request: httpx.Request) -> httpx.Response:
    assert "X-Storage-Service-Token" not in request.headers
    assert request.headers["X-Signed"] == "required"
    if request.method == "PUT":
        assert request.content == b"example"
    return httpx.Response(200, content=b"example")


def test_sync_example() -> None:
    transport, data_transport = ExampleTransport(control), ExampleTransport(data)
    result = upload_example(
        ClientConfig(base_url="https://storage.example", token=TOKEN),
        transport=transport,
        data_transport=data_transport,
    )
    assert result.size == 7
    assert transport.closed and data_transport.closed


@pytest.mark.asyncio
async def test_async_example(tmp_path: Path) -> None:
    transport, data_transport = ExampleTransport(control), ExampleTransport(data)
    target = tmp_path / "example.txt"
    target.write_text("previous")
    result = await download_example(
        ClientConfig(base_url="https://storage.example", token=TOKEN),
        target,
        transport=transport,
        data_transport=data_transport,
    )
    assert result.read_bytes() == b"example"
    assert transport.closed and data_transport.closed


def test_public_client_help() -> None:
    for client in (Client, AsyncClient):
        for name, method in inspect.getmembers(client, inspect.isfunction):
            if not name.startswith("_"):
                assert inspect.getdoc(method), f"{client.__name__}.{name} 缺少公共说明"
    assert "max_attempts" in (inspect.getdoc(ClientConfig) or "")
