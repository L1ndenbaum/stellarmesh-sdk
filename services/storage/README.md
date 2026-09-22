# Storage 控制面服务

为项目提供对象元数据与预签名请求，对象字节直接往返 S3／MinIO。运行前需要外部 Bucket、最小权限凭据和只读访问文件；SDK 不安装或管理对象存储。

安装镜像的固定版本和 digest 从[发布矩阵](../../docs/release.md#当前制品矩阵)及验收记录获取。不要从源码版本推断镜像已发布。

首次启动按[配置与排障指南](../../docs/storage-service.md#首次启动与排障路径)完成准备、live／ready 检查和[Python 示例](../../sdk/python/storage/tests/example_storage.py)的一次传输。配置文件示例由 Schema 检查，示例客户端在本地 HTTP 替身上执行；真实服务与 MinIO 链路由 `make integration-storage` 单独验证。

协议以 [Storage v1](../../contracts/storage/v1/README.md) 为准。服务不创建 Bucket、不持有管理员／迁移凭据，不自动运行迁移；部署与生产资源由业务或基础设施仓库拥有。
