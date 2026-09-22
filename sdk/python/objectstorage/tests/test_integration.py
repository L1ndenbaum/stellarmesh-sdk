"""仅由隔离 MinIO 入口启用，不读取业务 dotenv 或生产凭据。"""

import asyncio
import base64
import hashlib
import os
from collections.abc import Iterator
from contextlib import contextmanager
from datetime import UTC, datetime, timedelta
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from threading import Thread
from typing import Any
from unittest.mock import patch
from uuid import uuid4

import aioboto3
import boto3
import httpx
import pytest
from example_objectstorage import roundtrip

from stellarmesh_objectstorage import (
    AsyncClient,
    Client,
    ClientConfig,
    CompletedPart,
    ForbiddenError,
    InvalidRequestError,
    NotFoundError,
    UnavailableError,
)

pytestmark = pytest.mark.skipif(
    not os.environ.get("OBJECTSTORAGE_TEST_ENDPOINT"), reason="需要隔离 MinIO 入口"
)


def configuration() -> ClientConfig:
    endpoint = os.environ["OBJECTSTORAGE_TEST_ENDPOINT"]
    return ClientConfig(
        bucket=os.environ["OBJECTSTORAGE_TEST_BUCKET"],
        region="us-east-1",
        prefix="integration/" + uuid4().hex,
        endpoint=endpoint,
        presign_endpoint=endpoint.replace("127.0.0.1", "localhost"),
        use_path_style=True,
    )


def test_sync_real_objects_signatures_versions_and_multipart(tmp_path: Path) -> None:
    config = configuration()
    with Client(config) as client, httpx.Client(trust_env=False) as http:
        client.check()
        key = "资料/a +%.txt"
        first = client.upload_bytes(key, b"first", metadata={"source": "test"})
        assert first.etag and first.version_id
        source = tmp_path / "source"
        source.write_bytes(b"second")
        checksum = base64.b64encode(hashlib.sha256(b"second").digest()).decode()
        assert client.upload_file(key, source, checksum_sha256=checksum).etag
        with pytest.raises(InvalidRequestError):
            client.upload_bytes("bad-checksum", b"other", checksum_sha256=checksum)
        target = tmp_path / "target"
        client.download_file(key, target, version_id=first.version_id)
        assert target.read_bytes() == b"first"
        signed = client.presign_get(key, version_id=first.version_id)
        assert (
            http.request(signed.method, signed.url, headers=signed.headers).content
            == b"first"
        )
        signed = client.presign_put(
            "signed",
            size=6,
            content_type="text/plain",
            metadata={"source": "signed"},
            checksum_sha256=base64.b64encode(
                hashlib.sha256(b"signed").digest()
            ).decode(),
        )
        response = http.request(
            signed.method, signed.url, headers=signed.headers, content=b"signed"
        )
        response.raise_for_status()
        assert client.stat("signed").metadata == {"source": "signed"}
        upload = client.create_multipart("large")
        parts = []
        for number, data in ((1, b"a" * (5 * 1024**2)), (2, b"end")):
            part = client.presign_part(upload.key, upload.upload_id, number)
            response = http.put(part.url, headers=part.headers, content=data)
            response.raise_for_status()
            parts.append(CompletedPart(number, response.headers["etag"]))
        assert client.complete_multipart("large", upload.upload_id, parts).etag
        assert client.stat("large").size == 5 * 1024**2 + 3
        abort = client.create_multipart("abort")
        client.abort_multipart(abort.key, abort.upload_id)
        with pytest.raises(NotFoundError):
            client.complete_multipart(
                abort.key, abort.upload_id, [CompletedPart(1, '"absent"')]
            )
        old = datetime.now(UTC) - timedelta(hours=2)
        with patch("botocore.auth.get_current_datetime", return_value=old):
            expired = client.presign_get(key, expires_in=60)
        assert http.get(expired.url).status_code == 403
        client.delete(key)
        with pytest.raises(NotFoundError):
            client.stat(key)
    denied = ClientConfig(
        bucket=config.bucket,
        region=config.region,
        prefix="forbidden",
        endpoint=config.endpoint,
        use_path_style=True,
    )
    with Client(denied) as client, pytest.raises(ForbiddenError):
        client.upload_bytes("file", b"denied")


@pytest.mark.asyncio
async def test_async_real_files_signatures_and_multipart(tmp_path: Path) -> None:
    config = configuration()
    assert await roundtrip(config, "example") == b"hello"
    async with (
        AsyncClient(config) as client,
        httpx.AsyncClient(trust_env=False) as http,
    ):
        await client.check()
        assert (await client.upload_bytes("bytes", b"bytes")).etag
        source = tmp_path / "source"
        source.write_bytes(b"file" * 1024**2)
        assert (await client.upload_file("file", source)).etag
        target = tmp_path / "target"
        await client.download_file("file", target)
        assert target.read_bytes() == source.read_bytes()
        async with client.open_object("file") as stream:
            async for chunk in stream.iter_chunks(10):
                assert chunk == b"filefilefi"
                break
        signed = await client.presign_put("signed", size=5)
        (
            await http.put(signed.url, headers=signed.headers, content=b"hello")
        ).raise_for_status()
        signed = await client.presign_get("signed")
        assert (await http.get(signed.url)).content == b"hello"
        upload = await client.create_multipart("multipart")
        part = await client.presign_part(upload.key, upload.upload_id, 1)
        response = await http.put(part.url, content=b"last")
        response.raise_for_status()
        result = await client.complete_multipart(
            upload.key, upload.upload_id, [CompletedPart(1, response.headers["etag"])]
        )
        assert result.etag and (await client.stat(upload.key)).size == 4
        upload = await client.create_multipart("abort")
        await client.abort_multipart(upload.key, upload.upload_id)
        await client.delete("file")
        with pytest.raises(NotFoundError):
            await client.stat("file")


@contextmanager
def retry_server() -> Iterator[tuple[str, list[int]]]:
    attempts: list[int] = []

    class Handler(BaseHTTPRequestHandler):
        def do_HEAD(self) -> None:
            attempts.append(1)
            self.send_response(503)
            self.send_header("Content-Length", "0")
            self.end_headers()

        def log_message(self, format: str, *args: Any) -> None:
            pass

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        yield f"http://127.0.0.1:{server.server_port}", attempts
    finally:
        server.shutdown()
        server.server_close()
        thread.join()


def test_botocore_total_attempts_are_not_multiplied() -> None:
    with retry_server() as (endpoint, attempts):
        config = ClientConfig(
            bucket="documents",
            region="us-east-1",
            endpoint=endpoint,
            use_path_style=True,
            max_attempts=2,
        )
        session = boto3.Session(aws_access_key_id="test", aws_secret_access_key="test")
        with Client(config, session=session) as client, pytest.raises(UnavailableError):
            client.check()
        assert len(attempts) == 2


@pytest.mark.asyncio
async def test_aiobotocore_total_attempts_are_not_multiplied() -> None:
    with retry_server() as (endpoint, attempts):
        config = ClientConfig(
            bucket="documents",
            region="us-east-1",
            endpoint=endpoint,
            use_path_style=True,
            max_attempts=2,
        )
        session = aioboto3.Session(
            aws_access_key_id="test", aws_secret_access_key="test"
        )
        async with AsyncClient(config, session=session) as client:
            with pytest.raises(UnavailableError):
                await client.check()
        assert len(attempts) == 2
    await asyncio.sleep(0)
