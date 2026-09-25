# Python 进程内对象存储

`stellarmesh-objectstorage` 直接连接 S3 兼容对象存储（含 RustFS），不依赖 Storage 服务。同步 `Client` 用于线程或同步 Worker，异步 `AsyncClient` 使用 aioboto3，适合异步业务入口。当前发布状态以[发布矩阵](../../release.md#当前制品矩阵)为准；首次发布前可从源码构建 wheel 验证。

## 安装与装配

```sh
pip install stellarmesh-objectstorage==0.1.0
```

需要 Python 3.11+、预先创建的 Bucket 和项目最小权限凭据。SDK 不创建 Bucket，不修改 Policy、CORS、Versioning、Lifecycle 或 ACL。

<!-- example: sdk/python/objectstorage/tests/example_objectstorage.py -->
```python
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
```
<!-- /example -->

业务创建 `ClientConfig(bucket="example-documents", region="us-east-1")` 并传入上面的完整示例。RustFS 再配置 `endpoint` 和 `use_path_style=True`。同步用法将 `async with`／`await` 对应替换为 `with Client(...)`／同步调用。

默认使用标准 AWS 凭据链；业务也可以注入 `boto3.Session` 或 `aioboto3.Session`。SDK 不读取业务 dotenv，静态凭据由业务配置加载后交给 Session，工作负载身份由标准凭据链解析。客户端只关闭自己创建的底层连接。

## 对象定位与结果

客户端固定绑定一个 Bucket 与可选 Prefix，操作只传逻辑 key。非空 Prefix 规范为单个结尾斜杠；key 不进行路径清理、URL 解码或空白裁剪。物理键最大 1024 UTF-8 字节。逻辑 namespace 的选择由业务适配器负责，Prefix 绑定不能代替存储端权限控制。

`upload_bytes`、`upload_file` 和 `complete_multipart` 返回 `WriteResult`，直接包含存储端 ETag 和可选版本 ID，不隐式 Stat。ETag 是不透明值，不能当作 MD5。完整属性通过 `stat()` 或 `open_object()` 的 `info` 取得；读取和删除支持可选 `version_id`，版本删除和删除标记遵循 Bucket 策略。

文件上传最多 5 GiB，使用有界内存，不隐式 Multipart。传输及重试期间不要修改源文件。文件下载要求父目录存在，使用同目录临时文件，成功后原子替换；故障或取消会清理暂存并保留原目标。

## 生命周期、取消与错误

同步客户端可以 `with` 或显式 `close()`；异步客户端必须由 `async with` 打开，并在应用生命周期内复用。客户端关闭前先停止在途请求和流；同步客户端不跨进程共享，异步客户端不跨事件循环共享。`open_object` 必须在对应上下文中消费，提前退出也会释放响应体。`read()` 默认读取全部剩余字节，大对象应使用 `iter_chunks()`。

连接和读取超时默认各 5 秒，表示传输阶段超时，不是总任务期限。Botocore 使用 `standard` 模式，`max_attempts` 默认 3，包含首次；SDK 不额外叠加重试。创建／完成 Multipart 等写操作同样可能由底层重试，失败或取消不能证明服务端未完成；业务应保存上传 ID 并制定核对与清理策略。已开始读取的响应流不会自动重新请求。

异步取消保持 `CancelledError`。本地文件写线程与关闭操作会先完成资源清理，再传播取消。错误使用 `StorageError` 的具名子类区分输入错误、未找到、无权限、冲突、前置条件失败、不可用和未打开／已关闭。原始 cause 可用于受控诊断，不应无条件写入日志；本地文件错误保持 `OSError`。

## 预签名与 Multipart

`presign_get`、`presign_put`、`presign_part` 返回包含 `url`、`method`、`headers`、`expires_at` 的 `PresignedRequest`。默认有效期 900 秒，允许 60～3600 秒；临时凭据提前到期也会使签名提前失效。预签名不证明对象或分片会话存在。

`endpoint` 用于后端网络，`presign_endpoint` 用于浏览器或外部调用方，后者省略时沿用前者；必须在签名前选择端点，不得事后替换域名。客户端执行完整请求，保留方法、媒体类型、声明大小和元数据头；浏览器的 Content-Length 由实际请求体生成。浏览器直传仍需存储端 CORS 配置。

Multipart 顺序为 `create_multipart` → `presign_part` → 调用方上传各分片 → `complete_multipart`。完成参数使用 `CompletedPart(part_number, etag)`，SDK 拷贝排序并拒绝重复编号；放弃时显式 `abort_multipart`。SDK 不维护业务上传会话、后台清理任务或恢复数据库，不自动中止调用方仍可能继续的会话。

## 验证与迁移边界

单元检查执行 `make python-objectstorage-check python-objectstorage-test`；真实 RustFS 执行 `make integration-objectstorage`，不启动 Storage 服务。测试覆盖同步／异步传输、签名头、内外端点、版本、权限、Multipart、过期签名、关闭和重试次数。真实 AWS、业务模型和生产部署须单独验收。

旧 `stellarmesh-storage` 仍是远程 Storage v1 客户端，不能只替换服务地址来切换。业务应替换 Infrastructure 适配器并保留逻辑 namespace 和已有物理对象位置；迁移完成及线上验收后再退役服务，不必迁移对象字节。旧包和服务本轮保持兼容。

单次上传及 PUT 预签名可传 `checksum_sha256`（32 字节 SHA-256 摘要的 base64 编码），由存储端验证完整性。它与业务 metadata 中的摘要字段不同；SDK 不将 ETag 当作摘要。
