"""异步首次接入；配置与项目凭据由调用方准备。"""

from stellarmesh_objectstorage import AsyncClient, ClientConfig, StorageError


async def roundtrip(config: ClientConfig, key: str) -> bytes:
    """使用调用方指定的临时对象键，验证写入和流式读取后清理。"""
    try:
        async with AsyncClient(config) as storage:
            await storage.upload_bytes(key, b"hello", content_type="text/plain")
            try:
                async with storage.open_object(key) as stream:
                    return await stream.read()
            finally:
                await storage.delete(key)
    except StorageError:
        # 业务在此转换错误；不要记录签名 URL 或包含凭据的底层 cause。
        raise
