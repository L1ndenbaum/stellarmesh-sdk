# Python Storage

Python 3.11 及以上；需要部署好的 Storage 控制面、项目 token，以及可达的 S3／MinIO 数据面。SDK 不安装或部署这些依赖。

## 安装

示例适用于 `0.1.2`，当前发布状态见[发布矩阵](https://github.com/L1ndenbaum/stellarmesh-sdk/blob/dev/docs/release.md#当前制品矩阵)。

```sh
python -m pip install stellarmesh-storage==0.1.2
```

## 最小完整示例

由业务构造 `ClientConfig(base_url=服务地址, token=业务安全注入值)`，再调用下面函数。需要 documents 空间和对应 read／write 权限；同步例子写入 example.txt，异步例子下载它。测试用 HTTPX 替身验证控制面、数据面与关闭动作，业务中省略 transport 参数。

<!-- example: sdk/python/storage/tests/example_storage.py -->
```python
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
```
<!-- /example -->

## 关键限制

客户端需用 with／async with 或显式 close／aclose 关闭。控制面 token 不发送给对象存储；签名 URL 与请求头必须保留。单次上传至多 5 GiB，分片生命周期由业务管理；下载成功会覆盖目标文件。

## 深入指南

[接入与迁移](https://github.com/L1ndenbaum/stellarmesh-sdk/blob/dev/docs/sdk/python/storage.md)、[架构边界](https://github.com/L1ndenbaum/stellarmesh-sdk/blob/dev/docs/sdk-content.md)、[贡献与验证](https://github.com/L1ndenbaum/stellarmesh-sdk/blob/dev/CONTRIBUTING.md)。源码和注释以主干为准，已发布制品行为以对应 tag 为准。
