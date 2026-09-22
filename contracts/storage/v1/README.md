# Storage v1 契约入口

- [openapi.yaml](openapi.yaml)：控制面请求、响应、错误和鉴权协议。
- [access-config.schema.json](access-config.schema.json)：服务访问文件的结构验证。
- [limits.json](limits.json)：Go 服务与 Python 客户端共用的限制。
- [有效访问配置](testdata/valid-access-config.json)和[无效配置集合](testdata/invalid-access-configs.json)：跨实现契约测试数据，值均为测试用途。

服务端内部 DTO 不属于可导入的公共 Go API。客户端只发送逻辑 namespace 与 key；Bucket、项目凭据与授权由服务端配置拥有。预签名结果必须按原 URL、方法及签名请求头使用，数据面不携带控制面 token。

接入见 [Python Storage](../../../docs/sdk/python/storage.md)、[Go Object Storage](../../../docs/sdk/go/object-storage.md)，运行与权限见 [Storage 服务](../../../docs/storage-service.md)。Schema 与 OpenAPI 是权威定义，本页不复制字段表。
