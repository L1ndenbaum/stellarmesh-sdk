import asyncio
from collections.abc import AsyncIterator
from contextlib import asynccontextmanager
from pathlib import Path
from types import SimpleNamespace
from typing import Any

import aioboto3
import pytest
from botocore.stub import Stubber

from stellarmesh_objectstorage import AsyncClient, ClientClosedError, ClientConfig


@pytest.mark.asyncio
async def test_native_async_upload_stat_delete_and_sign() -> None:
    session = aioboto3.Session(aws_access_key_id="test", aws_secret_access_key="test")
    async with session.client("s3", region_name="us-east-1") as provider:

        @asynccontextmanager
        async def create(*args: Any, **kwargs: Any) -> AsyncIterator[Any]:
            yield provider

        with Stubber(provider) as stub:
            stub.add_response(
                "put_object",
                {"ETag": "etag"},
                {
                    "Bucket": "documents",
                    "Key": "prefix/file",
                    "Body": b"data",
                    "ContentLength": 4,
                },
            )
            stub.add_response(
                "head_object",
                {"ContentLength": 4, "ETag": "etag"},
                {"Bucket": "documents", "Key": "prefix/file"},
            )
            stub.add_response(
                "delete_object", {}, {"Bucket": "documents", "Key": "prefix/file"}
            )
            config = ClientConfig(
                bucket="documents", region="us-east-1", prefix="prefix"
            )
            client = AsyncClient(config, session=SimpleNamespace(client=create))
            with pytest.raises(ClientClosedError):
                await client.check()
            async with client:
                assert (await client.upload_bytes("file", b"data")).etag == "etag"
                assert (await client.stat("file")).size == 4
                await client.delete("file")
                assert (await client.presign_get("file")).method == "GET"
            with pytest.raises(ClientClosedError):
                await client.check()
            stub.assert_no_pending_responses()


@pytest.mark.asyncio
async def test_cancel_download_cleans_partial_file_and_closes_stream(
    tmp_path: Path,
) -> None:
    entered = asyncio.Event()
    closed = False

    class Body:
        async def read(self, size: int | None) -> bytes:
            entered.set()
            await asyncio.Future[None]()
            return b""

        def close(self) -> None:
            nonlocal closed
            closed = True

    class Provider:
        async def get_object(self, **kwargs: Any) -> dict[str, Any]:
            return {"Body": Body(), "ContentLength": 100}

    @asynccontextmanager
    async def create(*args: Any, **kwargs: Any) -> AsyncIterator[Provider]:
        yield Provider()

    destination = tmp_path / "file"
    destination.write_bytes(b"old")
    async with AsyncClient(
        ClientConfig(bucket="documents", region="us-east-1"),
        session=SimpleNamespace(client=create),
    ) as client:
        task = asyncio.create_task(client.download_file("file", destination))
        await entered.wait()
        task.cancel()
        with pytest.raises(asyncio.CancelledError):
            await task
    assert closed and destination.read_bytes() == b"old"
    assert list(tmp_path.iterdir()) == [destination]


@pytest.mark.asyncio
async def test_partial_setup_and_repeated_cancel_close() -> None:
    closing, finish = asyncio.Event(), asyncio.Event()
    closed: list[str] = []
    calls = 0

    @asynccontextmanager
    async def create(*args: Any, **kwargs: Any) -> AsyncIterator[object]:
        nonlocal calls
        calls += 1
        if calls == 2:
            raise RuntimeError("setup failed")
        try:
            yield object()
        finally:
            closing.set()
            await finish.wait()
            closed.append("client")

    config = ClientConfig(
        bucket="documents",
        region="us-east-1",
        endpoint="http://internal.test",
        presign_endpoint="http://public.test",
    )
    client = AsyncClient(config, session=SimpleNamespace(client=create))
    task = asyncio.create_task(client.__aenter__())
    await closing.wait()
    task.cancel()
    await asyncio.sleep(0)
    task.cancel()
    finish.set()
    with pytest.raises(asyncio.CancelledError):
        await task
    assert closed == ["client"]
    await client.aclose()
