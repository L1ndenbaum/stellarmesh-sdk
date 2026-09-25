# Go Gateway 接入

Gateway 将路由、认证、授权、限流与代理装配为固定安全流水线。当前可安装版本见[发布矩阵](../../release.md#当前制品矩阵)。Session 及可插拔凭证提取要求 Gateway `v0.4.0` 或更新版本；旧版 `v0.3.1` 不包含这些接口。

首次使用阅读[首次装配](gateway-quickstart.md)，用[本地完整示例](../../../sdk/go/gateway/example_test.go)验证最小代理。业务鉴权与 Redis 等扩展见[配置指南](gateway-configuration.md)，升级见[迁移指南](gateway-migration.md)。

以下保留旧章节入口。

## 安装固定版本

见[安装固定版本](gateway-quickstart.md#安装固定版本)。

## 设计语义

见[设计语义](gateway-quickstart.md#设计语义)。

## 响应协议归项目所有

见[响应协议归项目所有](gateway-quickstart.md#响应协议归项目所有)。

## 完整组装示例

见[完整组装示例](gateway-quickstart.md#完整组装示例)。

## 原生可验证示例

见[原生可验证示例](gateway-quickstart.md#原生可验证示例)。

## 路由和 upstream

见[路由和 upstream](gateway-configuration.md#路由和-upstream)。

## 身份和策略扩展

见[身份和策略扩展](gateway-configuration.md#身份和策略扩展)。

## 客户端地址和 CORS

见[客户端地址和 CORS](gateway-configuration.md#客户端地址和-cors)。

## 错误、日志和观测

见[错误、日志和观测](gateway-configuration.md#错误日志和观测)。

## 健康检查和测试

见[健康检查和测试](gateway-configuration.md#健康检查和测试)。

## 从 `v0.1.0` 升级到 `v0.2.0`

见[从 `v0.1.0` 升级到 `v0.2.0`](gateway-migration.md#从-v010-升级到-v020)。

## `v0.3.1` 访问日志修正

见[`v0.3.1` 访问日志修正](gateway-migration.md#v031-访问日志修正)。

## 从 `v0.2.0` 升级到 `v0.3.0`

见[从 `v0.2.0` 升级到 `v0.3.0`](gateway-migration.md#从-v020-升级到-v030)。

## 可插拔凭证提取迁移

见[可插拔凭证提取迁移](gateway-migration.md#可插拔凭证提取迁移)。

## Redis Session 初版

见[Redis Session 初版](gateway-migration.md#redis-session-初版)。

## 源码组织与维护

见[Gateway 维护约定](../../contributing/gateway.md)。

<a id="默认标准库访问日志"></a>
默认日志见[标准库访问日志](gateway-configuration.md#默认标准库访问日志)。
