# Stellarmesh Python 对象存储 SDK

直接访问 AWS S3／MinIO 的小型进程内客户端，同步使用 Boto3，异步使用 aioboto3。
不需要 Storage 服务。要求 Python 3.11+，Bucket 和项目凭据由业务部署准备。

```sh
pip install stellarmesh-objectstorage==0.1.0
```

```python
from stellarmesh_objectstorage import Client, ClientConfig, StorageError

config = ClientConfig(bucket="example-documents", region="us-east-1")
try:
    with Client(config) as storage:
        result = storage.upload_bytes("hello.txt", b"hello", content_type="text/plain")
        print(result.etag)
except StorageError:
    raise
```

凭据默认使用标准 AWS 凭据链，也可以注入 Session；不要将项目长期凭据交给浏览器。
异步客户端使用 `async with AsyncClient(config) as storage`，对象流也必须使用上下文关闭。
单次上传限制 5 GiB，较大对象使用显式 Multipart；SDK 不管理业务上传会话或自动创建 Bucket。
写入超时或取消不代表服务端没有完成；ETag 是不透明值，不保证为 MD5。

完整接入、生命周期与验证边界见[中文指南](https://github.com/L1ndenbaum/stellarmesh-sdk/blob/dev/docs/sdk/python/objectstorage.md)。
