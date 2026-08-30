# Python 对象存储 SDK 接入教程

`stellarmesh-storage` 是独立于 `stellarmesh-logging` 发布的 Python 包，import package 为 `stellarmesh_storage`，要求 Python 3.11 及以上版本。它只访问项目自己的 `storage-service` 控制面，并通过服务返回的预签名请求直接与 S3 或 MinIO 传输对象内容。

## 1. 安装与配置

```sh
pip install stellarmesh-storage==0.1.1
```

```python
from stellarmesh_storage import Client, ClientConfig

config = ClientConfig(
    base_url="http://storage-service:8090",
    token="storage-project-service-token-00000001",
    timeout_seconds=5.0,
    max_attempts=3,
)

with Client(config) as client:
    info = client.stat("documents", "reports/2026.pdf")
```

模型拒绝未知字段和隐式类型转换。token 不进入配置对象的 repr、`model_dump()` 或校验错误，但业务代码仍不得主动记录 `config.token`。

客户端不读取业务项目的 settings、请求上下文、环境变量或部署目录。业务项目在自己的配置层解析地址和 Secret，再显式构造 `ClientConfig`。

## 2. 控制面能力

同步 `Client` 和异步 `AsyncClient` 提供相同能力：

- `stat`、`delete`；
- `presign_get`、`presign_put`；
- `create_multipart`、`presign_part`、`complete_multipart`、`abort_multipart`；
- `upload_bytes`、`upload_file`、`download_file`。

所有控制面请求只向 `storage-service` 发送 `X-Storage-Service-Token`。客户端使用独立的 HTTP 连接池执行预签名 URL，数据面不会继承 service token；如果异常响应试图把该头放入 signed headers，客户端会拒绝执行。

## 3. 单次上传与原子下载

```python
client.upload_bytes(
    "documents",
    "uploads/input.json",
    b'{"ok":true}',
    content_type="application/json",
    metadata={"source": "worker"},
)

client.upload_file(
    "documents",
    "reports/2026.pdf",
    "/work/report.pdf",
    content_type="application/pdf",
)

path = client.download_file(
    "documents",
    "reports/2026.pdf",
    "/work/result.pdf",
)
```

`upload_bytes` 和 `upload_file` 使用单次 Presigned PUT。超过 5 GiB 时客户端在发送控制面请求前拒绝，并提示改用显式 Multipart；不会自动切分并隐藏 UploadID。

`download_file` 在目标目录中创建临时文件，流式写完后使用原子替换更新目标。下载、写入或替换失败时会删除临时文件，不留下半文件，原有目标不会被提前截断。

## 4. 显式 Multipart

```python
import httpx

from stellarmesh_storage import CompletedPart

upload = client.create_multipart("documents", "large/model.bin")
parts: list[CompletedPart] = []
first_part = b"x" * (5 * 1024 * 1024)

try:
    signed = client.presign_part(
        "documents", "large/model.bin", upload.upload_id, 1
    )
    response = httpx.request(
        signed.method,
        signed.url,
        headers=[
            (name, value)
            for name, values in signed.headers.items()
            for value in values
        ],
        content=first_part,
    )
    response.raise_for_status()
    parts.append(CompletedPart(part_number=1, etag=response.headers["ETag"]))

    client.complete_multipart(
        "documents", "large/model.bin", upload.upload_id, parts
    )
except Exception:
    client.abort_multipart("documents", "large/model.bin", upload.upload_id)
    raise
```

非末尾 Part 必须满足 provider 的最小大小要求，S3 通常要求至少 5 MiB。调用方负责保存 UploadID、Part Number 和 ETag，并在业务取消或失败时明确 Abort。不要把 signed URL 或 headers 放入异常、日志和指标。

## 5. 异步客户端

```python
from stellarmesh_storage import AsyncClient

async with AsyncClient(config) as client:
    await client.upload_file("images", "originals/a.png", "/work/a.png")
    await client.download_file("images", "originals/a.png", "/work/result.png")
```

`AsyncClient` 使用异步控制面和数据面连接池，文件分块读写通过受控线程调用完成，避免把整个文件读入内存。关闭后，同步和异步客户端都会拒绝新请求。

## 6. 重试语义

默认总尝试次数为 3，只对传输错误以及 `408`、`429`、`500`、`502`、`503`、`504` 重试。同步和异步实现共享相同的状态判断、退避计算、响应解析和错误映射。

可以自动重试的控制面操作包括 Stat、Presign、Delete、Abort 和 PresignPart。字节或可重新打开文件的签名 PUT/GET 也可安全重放。CreateMultipart 和 CompleteMultipart 的结果在连接中断时可能已经生效，因此永不自动重试；业务需要通过自身状态或后续查询决定处理方式。

## 7. 稳定异常

非成功状态会转换为：

- `InvalidRequestError`、`PayloadTooLargeError`；
- `UnauthorizedError`、`ForbiddenError`；
- `NotFoundError`；
- `ConflictError`、`PreconditionFailedError`；
- `UnavailableError`；
- `ClientClosedError`。

异常字符串不包含 token、预签名 URL、provider 响应体或 signed headers 中的安全令牌。需要诊断时记录异常类别、HTTP 状态和业务自己的低基数操作名，不要记录请求 URL。
