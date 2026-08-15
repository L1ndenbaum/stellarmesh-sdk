# stellarmesh-storage

`stellarmesh-storage` 提供严格类型的同步与异步 Python 客户端，通过项目级 `storage-service` 获取预签名请求，并让对象字节直接在客户端与 S3 或 MinIO 之间传输。

```python
from stellarmesh_storage import Client, ClientConfig

config = ClientConfig(
    base_url="http://storage-service:8090",
    token="storage-project-service-token-00000001",
    timeout_seconds=5.0,
    max_attempts=3,
)

with Client(config) as client:
    client.upload_file("documents", "reports/a.pdf", "/work/a.pdf")
    client.download_file("documents", "reports/a.pdf", "/work/result.pdf")
```

包提供 Stat、Delete、预签名 GET/PUT、显式 Multipart、单次文件上传和原子文件下载。控制面 service token 不会进入数据面请求，CreateMultipart 与 CompleteMultipart 不会因不确定结果自动重试。

完整接入、重试和安全边界见仓库文档 `docs/sdk/python/storage.md`。
