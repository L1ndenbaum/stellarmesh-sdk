"""完整同步／异步示例；transport 参数用于本地验证，业务中可省略。"""

from pathlib import Path

import httpx

from stellarmesh_storage import (
    AsyncClient,
    Client,
    ClientConfig,
    ObjectInfo,
    StorageError,
)


def upload_example(
    config: ClientConfig,
    *,
    transport: httpx.BaseTransport | None = None,
    data_transport: httpx.BaseTransport | None = None,
) -> ObjectInfo:
    """需 documents 空间的 write/read 权限，写入 example.txt。"""
    try:
        with Client(
            config, transport=transport, data_transport=data_transport
        ) as client:
            client.upload_bytes("documents", "example.txt", b"example")
            return client.stat("documents", "example.txt")
    except StorageError as error:
        # 不记录 token、签名 URL 或底层响应；交给应用决定如何呈现错误。
        raise RuntimeError(f"对象上传失败：{type(error).__name__}") from error


async def download_example(
    config: ClientConfig,
    target: Path,
    *,
    transport: httpx.AsyncBaseTransport | None = None,
    data_transport: httpx.AsyncBaseTransport | None = None,
) -> Path:
    """需 read 权限和既有 example.txt；成功后会覆盖 target。"""
    try:
        async with AsyncClient(
            config, transport=transport, data_transport=data_transport
        ) as client:
            return await client.download_file("documents", "example.txt", target)
    except StorageError as error:
        raise RuntimeError(f"对象下载失败：{type(error).__name__}") from error
