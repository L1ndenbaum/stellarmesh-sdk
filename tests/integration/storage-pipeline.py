#!/usr/bin/env python3
"""通过 storage-service 控制面和预签名数据面验证完整 MinIO 流程。"""

from __future__ import annotations

import argparse
import json
import urllib.error
import urllib.request
from dataclasses import dataclass
from typing import Any


@dataclass(frozen=True)
class Response:
    status: int
    headers: dict[str, str]
    body: bytes


def control(
    base_url: str,
    token: str,
    path: str,
    payload: dict[str, Any],
    *,
    expected: int = 200,
) -> Any:
    encoded = json.dumps(payload, ensure_ascii=False, separators=(",", ":")).encode()
    request = urllib.request.Request(
        base_url + path,
        data=encoded,
        method="POST",
        headers={
            "Content-Type": "application/json",
            "X-Storage-Service-Token": token,
        },
    )
    try:
        with urllib.request.urlopen(request, timeout=10) as response:
            status = response.status
            body = response.read()
    except urllib.error.HTTPError as error:
        status = error.code
        body = error.read()
    if status != expected:
        raise AssertionError(f"{path} 状态码为 {status}，预期 {expected}")
    envelope = json.loads(body)
    if envelope["code"] != status:
        raise AssertionError(f"{path} envelope code 不一致")
    return envelope.get("data")


def execute(presigned: dict[str, Any], body: bytes | None = None) -> Response:
    headers: list[tuple[str, str]] = []
    for name, values in presigned["headers"].items():
        headers.extend((name, value) for value in values)
    request = urllib.request.Request(
        presigned["url"], data=body, method=presigned["method"]
    )
    for name, value in headers:
        request.add_header(name, value)
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            return Response(
                status=response.status,
                headers={
                    name.lower(): value for name, value in response.headers.items()
                },
                body=response.read(),
            )
    except urllib.error.HTTPError as error:
        return Response(
            status=error.code,
            headers={name.lower(): value for name, value in error.headers.items()},
            body=error.read(),
        )


def verify(base_url: str, token: str, reader_token: str) -> None:
    control(
        base_url,
        "invalid-token",
        "/v1/objects/stat",
        {"namespace": "documents", "key": "single.bin"},
        expected=401,
    )
    control(
        base_url,
        token,
        "/v1/objects/stat",
        {"namespace": "unknown", "key": "single.bin"},
        expected=403,
    )
    control(
        base_url,
        reader_token,
        "/v1/objects/delete",
        {"namespace": "documents", "key": "single.bin"},
        expected=403,
    )

    payload = b"stellarmesh-storage-integration"
    upload = control(
        base_url,
        token,
        "/v1/presign/put",
        {
            "namespace": "documents",
            "key": "single.bin",
            "size": len(payload),
            "content_type": "application/octet-stream",
            "metadata": {"source": "integration"},
        },
    )
    upload_response = execute(upload, payload)
    if upload_response.status // 100 != 2:
        raise AssertionError(f"预签名 PUT 失败: {upload_response.status}")

    stat = control(
        base_url,
        token,
        "/v1/objects/stat",
        {"namespace": "documents", "key": "single.bin"},
    )
    if stat["size"] != len(payload) or stat["metadata"].get("source") != "integration":
        raise AssertionError("Stat 未返回预期 size 或 metadata")

    download = control(
        base_url,
        token,
        "/v1/presign/get",
        {"namespace": "documents", "key": "single.bin"},
    )
    downloaded = execute(download)
    if downloaded.status != 200 or downloaded.body != payload:
        raise AssertionError("预签名 GET 内容不一致")

    first_part = b"a" * (5 * 1024 * 1024)
    last_part = b"last-part"
    multipart = control(
        base_url,
        token,
        "/v1/multipart/create",
        {"namespace": "documents", "key": "multipart.bin"},
    )
    completed_parts: list[dict[str, Any]] = []
    for number, part in ((1, first_part), (2, last_part)):
        signed_part = control(
            base_url,
            token,
            "/v1/multipart/presign-part",
            {
                "namespace": "documents",
                "key": "multipart.bin",
                "upload_id": multipart["upload_id"],
                "part_number": number,
            },
        )
        part_response = execute(signed_part, part)
        etag = part_response.headers.get("etag", "")
        if part_response.status // 100 != 2 or not etag:
            raise AssertionError(f"Multipart Part {number} 上传失败")
        completed_parts.append({"part_number": number, "etag": etag})
    control(
        base_url,
        token,
        "/v1/multipart/complete",
        {
            "namespace": "documents",
            "key": "multipart.bin",
            "upload_id": multipart["upload_id"],
            "parts": completed_parts,
        },
    )
    multipart_download = control(
        base_url,
        token,
        "/v1/presign/get",
        {"namespace": "documents", "key": "multipart.bin"},
    )
    if execute(multipart_download).body != first_part + last_part:
        raise AssertionError("Multipart 完成后的内容不一致")

    aborted = control(
        base_url,
        token,
        "/v1/multipart/create",
        {"namespace": "documents", "key": "aborted.bin"},
    )
    control(
        base_url,
        token,
        "/v1/multipart/abort",
        {
            "namespace": "documents",
            "key": "aborted.bin",
            "upload_id": aborted["upload_id"],
        },
    )
    control(
        base_url,
        token,
        "/v1/multipart/complete",
        {
            "namespace": "documents",
            "key": "aborted.bin",
            "upload_id": aborted["upload_id"],
            "parts": [{"part_number": 1, "etag": "opaque"}],
        },
        expected=404,
    )

    version_id = stat.get("version_id")
    if not version_id:
        raise AssertionError("启用 Versioning 后 Stat 未返回 version_id")
    control(
        base_url,
        token,
        "/v1/objects/delete",
        {
            "namespace": "documents",
            "key": "single.bin",
            "version_id": version_id,
        },
    )
    control(
        base_url,
        token,
        "/v1/objects/stat",
        {"namespace": "documents", "key": "single.bin"},
        expected=404,
    )


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--base-url", required=True)
    parser.add_argument("--token", required=True)
    parser.add_argument("--reader-token", required=True)
    arguments = parser.parse_args()
    verify(arguments.base_url, arguments.token, arguments.reader_token)


if __name__ == "__main__":
    main()
