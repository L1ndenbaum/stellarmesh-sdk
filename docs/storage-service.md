# storage-service 部署与权限边界

`stellarmesh-storage-service` 是项目级对象存储控制面。每个项目独立部署一份实例并使用该项目自己的 AWS IAM Role、Web Identity 或 MinIO 项目凭据；同一实例可以声明多个逻辑 namespace，但所有 namespace 共用同一项目凭据池。

Storage v1 的公开、语言无关定义位于 `contracts/storage/v1`。服务端 Go DTO、限制校验、访问文件解析和 capability 策略位于 `services/storage/internal/storagev1`，属于镜像实现细节；业务 Go Module不能导入这个 `internal` package。进程内 Go 服务若直接持有项目凭据，应使用 `objectstorage`，而不是复用控制面认证模型。

镜像的已发布版本及验收摘要见[发布矩阵](release.md#当前制品矩阵)与[发布历史](releases/history.md)。生产环境必须引用已经验证的 manifest digest；公开镜像可匿名拉取，运行服务不需要 GitHub 发布凭据。

服务不代理对象字节、不写临时文件，也不提供对象内容 GET/PUT 路由。Go 服务可以直接使用进程内 `objectstorage` SDK；Python、浏览器或其他客户端通过控制面获得预签名请求后直接与 S3 或 MinIO 传输内容。

```text
业务客户端
  -> storage-service：token、namespace、key、元数据
  <- storage-service：Method、URL、Signed Headers、ExpiresAt
  -> S3/MinIO：对象字节
```

## 1. HTTP API

公开健康端点为：

- `GET /health`、`GET /health/live`：只报告进程存活；
- `GET /health/ready`：最近一次全部 namespace 检查均成功时返回 `200`，否则返回 `503`；
- `GET /metrics`：Prometheus 文本指标。

受保护控制面为：

- `POST /v1/objects/stat`、`POST /v1/objects/delete`；
- `POST /v1/presign/get`、`POST /v1/presign/put`；
- `POST /v1/multipart/create`、`POST /v1/multipart/presign-part`；
- `POST /v1/multipart/complete`、`POST /v1/multipart/abort`。

业务请求使用 `X-Storage-Service-Token`，JSON 中只出现逻辑 `namespace` 和 `key`，不能出现 Bucket。请求体最大 64 KiB，所有结构拒绝未知字段。完整结构见 [Storage OpenAPI](../contracts/storage/v1/openapi.yaml)，客户端操作参考留在各语言指南。

## 2. 访问配置

服务从只读挂载文件加载 namespace 与客户端授权。以下均为虚构值，不能作为真实凭据使用；[配置源码](examples/storage-access.json)通过[权威 Schema](../contracts/storage/v1/access-config.schema.json)与现有契约测试验证：

<!-- example: docs/examples/storage-access.json -->
```json
{
  "namespaces": {
    "documents": {"bucket": "project-documents", "prefix": ""},
    "images": {"bucket": "project-images", "prefix": "originals/"}
  },
  "principals": {
    "backend": {
      "tokens": [
        "storage-backend-current-token-00000001",
        "storage-backend-next-token-0000000001"
      ],
      "grants": {
        "documents": ["read", "write", "delete"],
        "images": ["read", "write"]
      }
    }
  }
}
```
<!-- /example -->

权限映射固定为：

| capability | 允许操作 |
| --- | --- |
| `read` | Stat、PresignGet |
| `write` | PresignPut 和全部 Multipart 生命周期 |
| `delete` | Delete |

token 至少 32 个 Unicode 字符。同一 principal 可以同时配置多个 token 完成滚动轮换；原始 token 只在启动解析期间存在，运行策略仅保存 SHA-256 digest，并对全部候选执行常量时间比较。重复 token、未知 capability、grant 引用未知 namespace、空 principal 或空授权都会阻止启动。

未知 token 返回 `401`；已认证但无 namespace 或 capability 权限返回 `403`。访问文件是应用层授权，不替代 IAM 或 MinIO Policy。

## 3. 环境变量

| 配置键 | 默认值 | 说明 |
| --- | --- | --- |
| `STELLARMESH_STORAGE_ADDR` | `:8090` | HTTP 监听地址 |
| `STELLARMESH_STORAGE_ACCESS_FILE` | 无 | 必填，只读访问配置文件 |
| `STELLARMESH_STORAGE_ENDPOINT` | 空 | S3-compatible 内部 Endpoint；AWS 模式留空 |
| `STELLARMESH_STORAGE_PRESIGN_ENDPOINT` | 空 | 客户端可访问的签名 Endpoint；为空继承内部 Endpoint |
| `STELLARMESH_STORAGE_USE_PATH_STYLE` | `false` | MinIO 常用 `true`，AWS 默认 `false` |
| `STELLARMESH_STORAGE_DEFAULT_PRESIGN_TTL` | `15m` | 默认预签名有效期 |
| `STELLARMESH_STORAGE_MAX_PRESIGN_TTL` | `1h` | 最大预签名有效期 |
| `STELLARMESH_STORAGE_READ_HEADER_TIMEOUT` | `5s` | HTTP header 读取超时 |
| `STELLARMESH_STORAGE_READ_TIMEOUT` | `10s` | HTTP 请求读取超时 |
| `STELLARMESH_STORAGE_WRITE_TIMEOUT` | `15s` | HTTP 响应写入超时 |
| `STELLARMESH_STORAGE_IDLE_TIMEOUT` | `60s` | HTTP keep-alive 空闲超时 |
| `STELLARMESH_STORAGE_SHUTDOWN_TIMEOUT` | `10s` | 优雅关闭上限 |
| `STELLARMESH_STORAGE_S3_CHECK_TIMEOUT` | `5s` | 单个 namespace Check 超时 |
| `STELLARMESH_STORAGE_S3_CHECK_INTERVAL` | `30s` | 全量 readiness 检查间隔 |

Region 和凭据使用标准 AWS 配置链。至少提供 `AWS_REGION` 或 `AWS_DEFAULT_REGION`；生产优先使用 Role 或 Web Identity。MinIO 开发环境可以注入 `AWS_ACCESS_KEY_ID`、`AWS_SECRET_ACCESS_KEY` 和可选 `AWS_SESSION_TOKEN`。

端点、TTL、访问文件或授权非法时服务直接退出。只设置 `STELLARMESH_STORAGE_PRESIGN_ENDPOINT` 而不设置内部 Endpoint 会被拒绝。生产对外预签名地址通常是 HTTPS 域名；CORS 是否允许浏览器直传由 Bucket 或对象存储网关配置负责。

## 日志格式与级别

从镜像 `0.3.0` 起，服务读取 `LOG_FORMAT=pretty|json` 和 `LOG_LEVEL=debug|info|warn|warning|error`，默认 `pretty` 与 `info`。本地可直接阅读文本，生产或采集验收显式使用 JSON；修改环境配置后重新创建容器。非法参数导致非零退出，不回显非法输入。

服务入口通过标准库 `slog.TextHandler`／`slog.JSONHandler` 选择展示，并复用 Go Logging `v0.4.0` 的安全 Handler。两种格式具有同样的字段脱敏和预算；SDK 不扫描消息或 error 文本中的凭据，应用不能把凭据拼接进消息。日志在业务配置读取前初始化，启动失败与 HTTP server 错误使用 ERROR，正常启动使用 INFO；不额外记录请求正文或访问日志。

这项依赖只属于可执行服务，`objectstorage` SDK 不增加日志依赖。服务不读取 dotenv 文件；环境变量、输出展示和生产采集启用由调用项目负责。这里没有 `LOG_ENABLED`，格式选择也不自动启用 Collector。

## 4. readiness 与 fail-close

readiness 初始为 false。服务启动后立即对全部 namespace 执行只读 `HeadBucket` 检查，并按配置间隔重复；全部成功后才变为 ready。任何一个 namespace 失败都会把聚合状态降为 false。受保护路由在认证后检查 readiness，not-ready 时直接返回 `503`，不会继续签名或调用 S3。

收到 `SIGINT` 或 `SIGTERM` 后，服务先标记 not-ready，再在关闭超时内停止 HTTP server 和健康检查循环。服务没有本地缓存、离线写入、失败 spool 或备用 Bucket，不会在 S3 不可用时降级到不受控位置。

错误状态固定为：

| 状态 | 含义 |
| --- | --- |
| `400` | 请求结构、key、TTL、Metadata、Checksum 或 Multipart 参数无效 |
| `401` | token 无效 |
| `403` | namespace 或 capability 无权 |
| `404` | 对象、版本或 UploadID 不存在 |
| `409` | provider 冲突 |
| `412` | 条件请求失败 |
| `413` | 控制面 JSON 超过 64 KiB |
| `503` | not-ready、超时、S3 权限错误、网络错误或未知 provider 错误 |

S3 权限错误统一对外返回无内部细节的 `503`，避免向客户端区分凭据、Bucket 或 Endpoint。指标标签只包含 route、status、operation 和 result，不包含 Bucket、key、principal、namespace 或 URL。

## 5. 生产资源责任

`storage-service` 和 SDK 不创建或修改 Bucket、CORS、Policy、Lifecycle、Versioning、ACL，也不持有管理员凭据。业务部署或 `server-infrastructure` 负责：

- 预先创建项目 Bucket，并决定是否启用 Versioning；
- 为项目 Role 或 MinIO 用户授予 namespace Prefix 的最小对象权限，以及只读 Bucket 检查权限；
- 为浏览器直传设置精确 Origin、Method 和 Header 的 CORS；
- 配置未完成 Multipart 清理和业务保留策略；
- 以 Secret 文件或工作负载身份注入访问配置和凭据；
- 固定镜像 digest、网络、TLS、资源限制、监控和发布顺序。

同一 `storage-service` 实例不能成为多项目中央凭据池。多个项目即使访问同一对象存储集群，也应独立部署实例、凭据、访问文件和网络策略。

## 6. 验证

仓库默认集成入口创建临时 Docker network，使用 RustFS `1.0.0` 官方多架构镜像并固定 SHA-256 digest。测试初始化使用 RustFS CLI `v0.1.36`，下载后校验固定摘要，在临时目录保存凭据配置；建立测试 Bucket、版本控制、项目用户和限定前缀的 Policy，并验证项目账号不能建桶。具体镜像引用位于 `tests/integration/storage-rustfs.sh`，CLI 初始化位于 `tests/integration/rustfs-setup.sh`，不依赖 MinIO／mc 镜像或可变 `latest` 标签。入口要求 Linux amd64／arm64、Docker、curl、tar 和 sha256sum；不提供生产部署配置，也不升级其他仓库已部署的 Storage 服务：

```sh
make integration
```

只运行对象存储流水线：

```sh
./tests/integration/storage-rustfs.sh
```

真实 AWS 手动入口要求调用方预先提供测试 Bucket、Prefix 和 Role，不创建 Bucket，也不在默认 CI 执行：

```sh
AWS_REGION=ap-southeast-1 \
STELLARMESH_STORAGE_AWS_BUCKET=project-integration-bucket \
STELLARMESH_STORAGE_AWS_PREFIX='sdk-manual/' \
make integration-aws
```

手动入口会在给定 Prefix 下创建、读取并删除唯一测试对象。运行前必须确认身份只能访问测试范围。

## 首次启动与排障路径

首次启动前，部署方须准备 Bucket／最小权限 Policy、运行身份、只读访问文件、镜像 digest，以及客户端能够访问的签名 endpoint。按本文配置表注入设置后启动镜像，先查 `/health/live`，再查 `/health/ready`，最后用[Python 完整示例](../sdk/python/storage/tests/example_storage.py)完成一次控制面签名与数据面传输。这里是操作步骤，文档检查不会启动或部署服务。

| 现象 | 检查顺序 |
| --- | --- |
| 启动失败 | 访问文件 Schema、重复 token、未知 capability、region 与正数超时；日志不要输出配置全文 |
| live 正常、ready 为 503 | 检查每个 namespace 的 Bucket／Prefix、项目凭据、网络和 S3 权限；ready 使用周期检查的最近结果 |
| 控制面返回 401／403 | 分别核对 token 与 namespace／capability 授权；应用授权不能代替 Bucket Policy |
| 签名成功但直传失败 | 客户端可达的 endpoint、原始 URL 与全部签名头、有效期、CORS；不要给数据面追加 service token |
| 上传完成但业务记录缺失 | SDK 不拥有业务事务和完成确认；按项目上传流程检查，不靠服务 readiness 推断业务成功 |

服务配置与权限说明在本页维护，线协议在[契约入口](../contracts/storage/v1/README.md)维护。服务不会自动执行资源迁移、创建 Bucket 或提供生产部署编排。
