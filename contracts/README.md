# 共享契约

契约是跨语言协议与清洗规则的唯一维护位置。SDK 指南说明用法，不能另行定义字段、默认限制或权限语义。

- [日志字段清洗](logging/sanitization.md)：Go／Python 共用，测试数据随契约维护。
- [Storage v1](storage/v1/README.md)：OpenAPI、访问配置 Schema、限制和验证样例。
- `logging/v1/`、`logging/v2/`：冻结历史，仅供旧运行时迁移，不是新项目接入入口，不修改内容。

契约变更须同时检查所有仍实现该能力的语言与服务；文档整理不得借机修改协议。历史制品和迁移边界见[发布说明](../docs/release.md)。
