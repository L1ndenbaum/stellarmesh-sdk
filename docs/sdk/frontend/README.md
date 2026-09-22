# 前端 HTTP／SSE SDK 接入

本文示例适用于 `@stellarmesh/sdk@0.3.0`，当前状态见[发布矩阵](../../release.md#当前制品矩阵)。SDK 只负责传输与通用认证协调，DTO、页面状态、业务终态归项目所有。

```sh
npm install @stellarmesh/sdk@0.3.0
```

首次接入从[完整请求示例](../../../sdk/frontend/examples/quickstart.ts)开始；运行前需要提供返回 JSON 的 `/items` 服务。示例由隔离 tarball 消费测试用本地服务执行。各主题中的业务代码是装配片段，实际回调由项目提供。

| 任务 | 指南 |
| --- | --- |
| 基础请求与响应 | [基础请求与响应](http.md) |
| 会话与认证恢复 | [会话与认证恢复](auth.md) |
| SSE 流式消费 | [SSE 流式消费](sse.md) |
| 对象传输与错误处理 | [对象传输与错误处理](errors-and-transfer.md) |
| 前端迁移说明 | [前端迁移说明](migration.md) |

维护者阅读[前端构建与源码约定](../../contributing/frontend.md)。以下保留原章节入口。

## 声明式接口

已归入[声明式接口](http.md#声明式接口)。

## metadata 返回

已归入[metadata 返回](http.md#metadata-返回)。

## 动态路径和通用请求声明

已归入[动态路径和通用请求声明](http.md#动态路径和通用请求声明)。

## 配置派生

已归入[配置派生](http.md#配置派生)。

## 信封和响应类型

已归入[信封和响应类型](http.md#信封和响应类型)。

## 超时、重试和取消

已归入[超时、重试和取消](http.md#超时重试和取消)。

## 鉴权和并发刷新

已归入[鉴权和并发刷新](auth.md#鉴权和并发刷新)。

## Bearer 会话的条件提交示例

已归入[Bearer 会话的条件提交示例](auth.md#bearer-会话的条件提交示例)。

## 浏览器 Cookie Session 示例

已归入[浏览器 Cookie Session 示例](auth.md#浏览器-cookie-session-示例)。

## SSE 流式接口

已归入[SSE 流式接口](sse.md#sse-流式接口)。

## 可配置错误码提取

已归入[可配置错误码提取](errors-and-transfer.md#可配置错误码提取)。

## 对象存储传输

已归入[对象存储传输](errors-and-transfer.md#对象存储传输)。

## 错误处理

已归入[错误处理](errors-and-transfer.md#错误处理)。

## 全声明式入口迁移

已归入[全声明式入口迁移](migration.md#全声明式入口迁移)。

## 未发布初版的认证接口迁移

已归入[未发布初版的认证接口迁移](migration.md#未发布初版的认证接口迁移)。

## 从 XieHe 接入

已归入[从 XieHe 接入](migration.md#从-xiehe-接入)。

## 构建与验证

见[前端维护指南](../../contributing/frontend.md)。

<a id="声明默认配置与调用配置"></a>
<a id="执行与生命周期"></a>

原配置与生命周期章节见[基础请求](http.md#声明默认配置与调用配置)。
