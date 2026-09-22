"""仅通过已安装的公开入口消费类型、签名和生命周期。"""

import asyncio
import os
from typing import assert_type

from stellarmesh_objectstorage import (
    AsyncClient,
    Client,
    ClientClosedError,
    ClientConfig,
    CompletedPart,
    MultipartUpload,
    ObjectInfo,
    PresignedRequest,
    WriteResult,
)


async def type_contract(client: AsyncClient) -> None:
    assert_type(await client.upload_bytes("key", b"data"), WriteResult)
    assert_type(await client.stat("key"), ObjectInfo)
    assert_type(await client.create_multipart("key"), MultipartUpload)
    assert_type(await client.presign_part("key", "upload", 1), PresignedRequest)
    assert_type(
        await client.complete_multipart("key", "upload", [CompletedPart(1, "etag")]),
        WriteResult,
    )
    async with client.open_object("key") as stream:
        assert_type(await stream.read(), bytes)


async def main() -> None:
    os.environ["AWS_ACCESS_KEY_ID"] = "consumer-test"
    os.environ["AWS_SECRET_ACCESS_KEY"] = "consumer-test-secret"
    config = ClientConfig(
        bucket="consumer",
        region="us-east-1",
        endpoint="https://s3.example.test",
        use_path_style=True,
    )
    with Client(config) as client:
        request = client.presign_get("测试.txt")
        assert_type(request, PresignedRequest)
        assert request.url.startswith("https://s3.example.test/consumer/")
    async with AsyncClient(config) as async_client:
        assert (await async_client.presign_get("key")).method == "GET"
    try:
        await async_client.check()
    except ClientClosedError:
        pass
    else:
        raise AssertionError("关闭后的客户端不得继续操作")


if __name__ == "__main__":
    asyncio.run(main())
