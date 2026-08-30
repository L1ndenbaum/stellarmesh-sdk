# Go 对象存储 SDK 接入教程

本教程对应独立 Module `github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/objectstorage` 与其中的 `objectstorage/s3store`。SDK 适用于进程内 Go 服务访问 AWS S3 或 S3-compatible 存储，并把一个客户端固定到唯一 Bucket 和可选 Prefix。业务代码每次只提供逻辑 key，不能在请求中切换 Bucket。

Namespace 是防误用边界，不是权限边界。生产权限仍必须由 AWS IAM Role 或 MinIO Policy 限制到当前项目拥有的 Bucket 和 Prefix。

本 package 不实现 `storage-service` 的 HTTP 协议，也不包含 service token、principal、capability、访问配置文件或项目级响应模型。需要让不持有对象存储凭据的客户端使用预签名控制面时，应部署 `storage-service` 并按 `contracts/storage/v1` 接入；不要把控制面 DTO 合并进 provider-neutral 的 `objectstorage` 接口。

## 1. 安装与凭据

业务项目固定 Go module 版本：

```sh
go get github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/objectstorage@v0.1.0
go mod tidy
```

如果项目还使用父 SDK，父版本必须至少是已经移除旧 Object Storage package 的 `v0.5.0`；`sdk/go/v0.4.0` 与独立 Object Storage Module 同时存在会造成 `ambiguous import`。

默认构造会调用 AWS SDK for Go v2 的标准配置链，支持环境变量、Web Identity、共享 Profile、ECS Role 和 EC2 Role。生产环境优先使用项目自己的 IAM Role 或工作负载身份，不要把长期 Access Key 写进源码、镜像或普通配置文件。

## 2. AWS S3 模式

```go
package storage

import (
    "context"
    "time"

    "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/objectstorage"
    "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/objectstorage/s3store"
)

func New(ctx context.Context) (*s3store.Client, error) {
    return s3store.New(ctx, s3store.Config{
        Region: "ap-southeast-1",
        Namespace: objectstorage.Namespace{
            Bucket: "project-documents",
            Prefix: "documents/",
        },
        DefaultPresignTTL: 15 * time.Minute,
        MaxPresignTTL:     time.Hour,
    })
}
```

AWS 模式不设置 `Endpoint`，默认使用 Virtual Hosted Style。`Config.Region` 总是覆盖注入 `aws.Config` 中的 Region。需要统一 Transport、测试凭据或预先加载 AWS 配置时，可以分别使用 `WithHTTPClient`、`WithCredentialsProvider` 和 `WithAWSConfig`；同一 Option 重复传入会在构造阶段报错。

## 3. MinIO 与双 Endpoint

容器内访问地址和返回给业务客户端的地址可能不同。例如 `storage-service` 在 Docker 网络内访问 `http://minio:9000`，浏览器或宿主机需要访问反向代理后的 HTTPS 地址：

```go
client, err := s3store.New(ctx, s3store.Config{
    Region: "us-east-1",
    Namespace: objectstorage.Namespace{
        Bucket: "project-documents",
        Prefix: "documents/",
    },
    Endpoint:        "http://minio:9000",
    PresignEndpoint: "https://objects.example.com",
    UsePathStyle:    true,
})
```

`PresignEndpoint` 为空时继承内部 `Endpoint`。只配置 `PresignEndpoint` 而不配置内部 `Endpoint` 会被拒绝，避免把 AWS 默认端点和自定义签名端点意外混用。自定义端点通过 AWS v2 的 `BaseEndpoint` 实现，不使用旧版 endpoint resolver。

## 4. Namespace 与 key

- Bucket 只在构造 `Namespace` 时出现；`Put`、`Get`、`Delete` 和预签名请求都不接受 Bucket。
- 空 Prefix 表示 Bucket 根。非空 Prefix 不能以 `/` 开头，SDK 会把多个结尾 `/` 规范化成一个。
- 逻辑 key 必须是有效 UTF-8、非空、无首尾空白、不能以 `/` 开头，也不能包含控制字符。
- SDK 不调用 `path.Clean`，因此不会改写 key 中间的 `//`、`.` 或 `..`；调用方需要自行定义业务命名规则。
- `Prefix + key` 的 UTF-8 字节数不能超过 1024。

如果项目需要多类对象，建议为每类稳定用途创建一个进程级客户端，例如 `documents` 和 `images`，不要在请求中接受用户提供的 Bucket。

## 5. 流式读写

单次上传必须提供已知长度，不创建临时文件：

```go
payload := []byte("example")
info, err := client.Put(ctx, objectstorage.PutRequest{
    Key:         "reports/2026.txt",
    Body:        bytes.NewReader(payload),
    Size:        int64(len(payload)),
    ContentType: "text/plain",
    Metadata:    map[string]string{"source": "reporter"},
})
```

`Put` 最大为 5 GiB，超出后返回 `ErrInvalidArgument`，调用方必须显式使用 Multipart。SDK 不隐藏 Multipart 状态，也不会在失败时自动把对象内容写到本地文件。

读取对象时，调用方拥有并必须关闭响应体：

```go
end := int64(1023)
object, err := client.Get(ctx, objectstorage.GetRequest{
    Object: objectstorage.ObjectRef{Key: "reports/2026.txt"},
    Range:  &objectstorage.ByteRange{Start: 0, End: &end},
})
if err != nil {
    return err
}
defer object.Body.Close()
```

`Range` 支持完整下载、`start-` 和 `start-end`。`ObjectInfo` 返回逻辑 key、VersionID、ETag、大小、Content-Type、最后修改时间、Metadata 和可用 Checksum。ETag 是不透明标识，尤其在 Multipart 或加密场景中不能当作 MD5。

Checksum 支持 CRC32、CRC32C、SHA1 和 SHA256，值使用 S3 规范的标准 Base64。一次请求只设置一种；未设置时由 AWS SDK 和 provider 的默认策略处理。

## 6. 预签名直传

```go
signed, err := client.PresignPut(ctx, objectstorage.PresignPutRequest{
    Key:         "uploads/input.bin",
    Size:        contentLength,
    ContentType: "application/octet-stream",
    TTL:         10 * time.Minute,
})
```

返回值包含 `Method`、`URL`、完整 `Headers` 和 `ExpiresAt`。执行数据面请求时必须原样使用这些 signed headers，不要额外加入控制面 token，也不要把 URL 写入日志、指标或异常。TTL 默认 15 分钟，可配置范围为 1 分钟到 1 小时。

## 7. 显式 Multipart

Multipart 生命周期固定为：

```text
CreateMultipart
  -> 按需 PresignPart
  -> 客户端上传每个 Part 并保存 ETag
  -> CompleteMultipart
失败或取消 -> AbortMultipart
```

Part Number 范围是 `1..10000`。`CompleteMultipart` 要求非空、无重复且每项 ETag 非空；SDK 会复制并排序分片，不修改调用方 slice。Create 只返回 UploadID，不提前生成一万个 URL。上传失败后的清理由调用方明确执行 `AbortMultipart`，或由业务部署配置的 Bucket Lifecycle 兜底。

## 8. 错误与观测

公共错误支持 `errors.Is`：

- `ErrInvalidArgument`：构造或请求参数非法；
- `ErrNotFound`：对象、版本、Bucket 或 UploadID 不存在；
- `ErrForbidden`：provider 拒绝凭据或权限；
- `ErrConflict`：资源状态冲突；
- `ErrPreconditionFailed`：条件请求失败；
- `ErrUnavailable`：超时、取消、网络或未知 provider 错误。

底层 Smithy 错误保留在 `Unwrap` 链中，但公开错误文本不会包含 Endpoint、Secret 或签名 URL。`WithObserver` 只收到 operation、outcome、duration、bytes 和错误类别，不会收到 Bucket、完整 key 或 URL。

`Check` 只执行只读 Bucket 可访问性检查，绝不创建 Bucket。客户端使用调用方 context 和 AWS SDK 标准有限重试，不创建替代业务 context，也不叠加无限重试。

## 9. 明确不提供的能力

SDK 不提供 Bucket 创建、List、Copy、ACL、CORS、Policy、Lifecycle、Versioning 或本地失败 spool。Bucket 与治理配置属于业务部署或 `server-infrastructure`；List 和 Copy 若成为稳定跨项目需求，应先形成明确的分页、权限、成本和幂等契约，再扩展公共接口。
