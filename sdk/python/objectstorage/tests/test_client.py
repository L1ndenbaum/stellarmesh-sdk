import io
from collections.abc import Iterator
from pathlib import Path
from types import SimpleNamespace
from typing import Any
from urllib.parse import parse_qs, urlsplit

import boto3
import pytest
from botocore.response import StreamingBody
from botocore.stub import ANY, Stubber

from stellarmesh_objectstorage import (
    Client,
    ClientClosedError,
    ClientConfig,
    CompletedPart,
    ConflictError,
    ForbiddenError,
    InvalidRequestError,
    NotFoundError,
    PreconditionFailedError,
    UnavailableError,
)


@pytest.fixture
def connection() -> Iterator[tuple[Client, Stubber]]:
    provider = boto3.Session(
        aws_access_key_id="test", aws_secret_access_key="test"
    ).client("s3", region_name="us-east-1", endpoint_url="http://objects.test")
    config = ClientConfig(bucket="documents", region="us-east-1", prefix="tenant")
    with (
        Stubber(provider) as stub,
        Client(
            config, session=SimpleNamespace(client=lambda *a, **kw: provider)
        ) as client,
    ):
        yield client, stub
        stub.assert_no_pending_responses()


def test_upload_returns_provider_result_without_extra_stat(
    connection: tuple[Client, Stubber],
) -> None:
    client, stub = connection
    stub.add_response(
        "put_object",
        {"ETag": '"opaque"', "VersionId": "v1"},
        {
            "Bucket": "documents",
            "Key": "tenant/file",
            "Body": b"data",
            "ContentLength": 4,
            "ContentType": "text/plain",
            "Metadata": {"source": "test"},
        },
    )
    result = client.upload_bytes(
        "file", b"data", content_type="text/plain", metadata={"Source": "test"}
    )
    assert (result.etag, result.version_id) == ('"opaque"', "v1")


def test_file_upload_and_atomic_download(
    connection: tuple[Client, Stubber], tmp_path: Path
) -> None:
    client, stub = connection
    source = tmp_path / "source"
    source.write_bytes(b"content")
    stub.add_response(
        "put_object",
        {"ETag": "etag"},
        {"Bucket": "documents", "Key": "tenant/file", "Body": ANY, "ContentLength": 7},
    )
    assert client.upload_file("file", source).etag == "etag"
    body = io.BytesIO(b"content")
    stub.add_response(
        "get_object",
        {"Body": StreamingBody(body, 7), "ContentLength": 7},
        {"Bucket": "documents", "Key": "tenant/file", "VersionId": "v1"},
    )
    target = tmp_path / "target"
    target.write_bytes(b"old")
    info = client.download_file("file", target, version_id="v1")
    assert info.key == "file" and info.size == 7
    assert target.read_bytes() == b"content" and body.closed
    assert sorted(path.name for path in tmp_path.iterdir()) == ["source", "target"]


def test_failed_stream_preserves_target_and_releases_body(
    connection: tuple[Client, Stubber], tmp_path: Path
) -> None:
    client, stub = connection
    body = io.BytesIO(b"short")
    stub.add_response(
        "get_object", {"Body": StreamingBody(body, 100), "ContentLength": 100}
    )
    target = tmp_path / "target"
    target.write_bytes(b"old")
    with pytest.raises(UnavailableError):
        client.download_file("file", target)
    assert target.read_bytes() == b"old" and body.closed
    assert list(tmp_path.iterdir()) == [target]


def test_early_stream_exit_and_closed_client(
    connection: tuple[Client, Stubber],
) -> None:
    client, stub = connection
    body = io.BytesIO(b"content")
    stub.add_response(
        "get_object", {"Body": StreamingBody(body, 7), "ContentLength": 7}
    )
    with client.open_object("file") as stream:
        assert next(stream.iter_chunks(1)) == b"c"
    assert body.closed
    with pytest.raises(ClientClosedError):
        stream.read()
    client.close()
    client.close()
    with pytest.raises(ClientClosedError):
        client.check()


@pytest.mark.parametrize(
    "status,code,error",
    [
        (404, "NoSuchKey", NotFoundError),
        (403, "AccessDenied", ForbiddenError),
        (503, "SlowDown", UnavailableError),
        (400, "BadDigest", InvalidRequestError),
        (409, "ConditionalRequestConflict", ConflictError),
        (412, "PreconditionFailed", PreconditionFailedError),
    ],
)
def test_error_mapping_keeps_cause_without_diagnostic_leak(
    connection: tuple[Client, Stubber], status: int, code: str, error: type[Exception]
) -> None:
    client, stub = connection
    stub.add_client_error("head_object", code, "secret-provider-url", status)
    with pytest.raises(error) as caught:
        client.stat("file")
    assert caught.value.__cause__ is not None
    assert "secret" not in str(caught.value)


def test_multipart_complete_sorts_without_mutation(
    connection: tuple[Client, Stubber],
) -> None:
    client, stub = connection
    stub.add_response(
        "create_multipart_upload",
        {"UploadId": "upload"},
        {"Bucket": "documents", "Key": "tenant/file"},
    )
    assert client.create_multipart("file").upload_id == "upload"
    parts = [CompletedPart(2, "second"), CompletedPart(1, "first")]
    stub.add_response(
        "complete_multipart_upload",
        {"ETag": "joined"},
        {
            "Bucket": "documents",
            "Key": "tenant/file",
            "UploadId": "upload",
            "MultipartUpload": {
                "Parts": [
                    {"PartNumber": 1, "ETag": "first"},
                    {"PartNumber": 2, "ETag": "second"},
                ]
            },
        },
    )
    assert client.complete_multipart("file", "upload", parts).etag == "joined"
    assert parts[0].part_number == 2
    with pytest.raises(InvalidRequestError):
        client.complete_multipart("file", "upload", [parts[0], parts[0]])


def test_public_endpoint_signing_uses_complete_headers() -> None:
    config = ClientConfig(
        bucket="documents",
        region="us-east-1",
        endpoint="http://internal.test",
        presign_endpoint="https://public.test",
        prefix="project",
        use_path_style=True,
    )
    session = boto3.Session(aws_access_key_id="test", aws_secret_access_key="test")
    with Client(config, session=session) as client:
        signed = client.presign_put(
            "资料/a +.txt",
            size=7,
            content_type="text/plain",
            metadata={"Origin": "example"},
        )
        assert urlsplit(signed.url).hostname == "public.test"
        query = parse_qs(urlsplit(signed.url).query)
        assert query["X-Amz-Expires"] == ["900"]
        assert query["X-Amz-SignedHeaders"] == [
            "content-length;content-type;host;x-amz-meta-origin"
        ]
        assert signed.headers == {
            "Content-Length": "7",
            "Content-Type": "text/plain",
            "x-amz-meta-origin": "example",
        }
        assert "Signature" not in repr(signed)


def test_transport_configuration_and_partial_construction_cleanup() -> None:
    closed: list[str] = []
    options: list[dict[str, Any]] = []

    def create(_service: str, **kwargs: Any) -> Any:
        options.append(kwargs)
        if len(options) == 2:
            raise RuntimeError("setup failed")
        return SimpleNamespace(close=lambda: closed.append("client"))

    config = ClientConfig(
        bucket="documents",
        region="us-east-1",
        endpoint="http://internal.test",
        presign_endpoint="http://public.test",
        max_attempts=2,
    )
    with pytest.raises(RuntimeError):
        Client(config, session=SimpleNamespace(client=create))
    assert closed == ["client"]
    assert options[0]["config"].retries == {"mode": "standard", "total_max_attempts": 2}
