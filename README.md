# Stellarmesh SDK

提供跨项目复用的轻量 SDK、声明式 Go 网关和项目级对象存储控制面。业务项目拥有 DTO、会话策略、日志投影和部署配置；SDK 通过公开配置和可注入接口接入这些能力。

## 接入组件

从 [SDK 选择与接入](docs/sdk/README.md)选择组件，沿着“安装 → 最小示例 → 场景指南 → 迁移说明”完成接入。各组件独立发布，当前可安装版本统一查阅[发布矩阵](docs/release.md#当前制品矩阵)，源码版本不代表制品已发布。

- 理解组件组合与边界：[架构说明](docs/sdk-content.md)。
- 将 SDK 接入业务系统：[跨组件接入](docs/sdk-integration.md)。
- 部署对象存储控制面：[Storage 服务](docs/storage-service.md)。
- 查阅权威协议：[共享契约](contracts/README.md)。

## 参与维护

从[贡献指南](CONTRIBUTING.md)准备环境和运行验证；文档、注释与示例遵循[维护规范](docs/documentation.md)。发布操作与历史记录分别由[发布入口](docs/release.md)和[历史记录](docs/releases/history.md)维护。

## 生产责任边界

本仓库拥有 SDK 与 Storage v1 协议。日志默认使用语言标准库输出，再由项目选择 Collector 和数据库投影；旧 Logging v1/v2 仅为迁移冻结保留。生产资源、数据库迁移、Bucket、Policy、CORS、Secret 和发布顺序由业务部署或基础设施仓库管理。本仓库不提供 Compose 或生产环境文件，服务不持有管理员／迁移凭据，不自动创建 Bucket 或执行迁移。

<a id="仓库内容"></a>
<a id="本地验证"></a>

旧内容与验证入口分别迁至 [SDK 目录](docs/sdk/README.md)和[贡献指南](CONTRIBUTING.md#环境与验证)。
