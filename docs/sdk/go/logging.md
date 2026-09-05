# Go Logging SDK 接入教程

本教程对应独立Module：

```text
github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging
```

本教程对应待发布的 `v0.4.0`；当前公共版本仍是 `v0.3.0`。`v0.4.0` 要求Go 1.24及以上，只依赖标准库。它装饰项目已有的`slog.Handler`，提供脱敏和有界化；它不创建Logger、不选择stdout/stderr、不实现远程Client，也不定义项目字段或数据库Schema。

## 安装

发布前在本仓库通过 `go.work` 验证；以下命令仅在 `v0.4.0` 正式发布后使用。

```sh
go get github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging@v0.4.0
go mod tidy
```

## 基础配置

```go
package main

import (
    "log/slog"
    "os"

    stellarlogging "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging"
)

func main() {
    level := new(slog.LevelVar)
    level.Set(slog.LevelInfo)

    base := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
        Level:     level,
        AddSource: true,
    })
    safe, err := stellarlogging.NewSanitizingHandler(
        base,
        stellarlogging.HandlerOptions{},
    )
    if err != nil {
        panic(err)
    }

    logger := slog.New(safe).With("service", "orders-api")
    logger.Info(
        "request completed",
        "request_id", "request-1",
        "duration_ms", 12.5,
    )
}
```

输出格式、最低级别、source和输出目标都由标准库Handler决定。需要全局Logger时由项目调用`slog.SetDefault(logger)`；SDK不会修改全局状态。

## 上下文字段

项目可以注入自己的request ID或trace ID：

```go
safe, err := stellarlogging.NewSanitizingHandler(base, stellarlogging.HandlerOptions{
    ContextAttrs: func(ctx context.Context) []slog.Attr {
        requestID, _ := ctx.Value(requestIDKey{}).(string)
        if requestID == "" {
            return nil
        }
        return []slog.Attr{slog.String("request_id", requestID)}
    },
})
```

`ContextAttrs` 只是一条注入接口，不规定 context key 或字段名。它和下游 Handler 的 panic 正常传播，SDK 不再导出两个 panic 错误类别。下游普通错误原样返回；标准 `slog.Logger` 不向业务日志调用者返回 Handler 错误，输出错误观测由项目负责。

## 字段清洗

完整规则以[跨语言清洗约定](../../../contracts/logging/sanitization.md)为准。默认消息和字符串各 `16KiB`、项目节点 `64`、深度 `8`。固定字段、上下文字段、当前属性、具名组和嵌套容器共享节点预算；`WithGroup` 不再绕过脱敏或深度，匿名组不额外计层级。

敏感字段按规范化后的名称精确匹配：`apiKey`、`API-KEY`、`access_token` 等会脱敏，`token_count`、`session_id` 不会被子串误伤。额外项目凭据名称通过 `ExtraSensitiveKeys` 配置。规则不扫描 message 和错误文本。

支持标准标量、时间、duration、error、字符串键 map、slice、array 和 `LogValuer`；普通业务 struct 或 pointer 不再自动 JSON 展开，也不调用未知对象的 `MarshalJSON` 或 `String()`。不支持的值输出 `[UNSERIALIZABLE]`。复杂对象应转换为少量明确字段，或实现标准库 `LogValuer`。

SDK 不修改调用方数据；`WithAttrs` 复制顶层列表，调用方不能在日志输出时并发修改嵌套容器。等级与输出格式继续由标准库控制，包括原生 `WARN` 等级名称。

## 从 `v0.3.0` 迁移

1. 检查依赖隐式 struct/pointer 编码的调用，改为显式属性或 `LogValuer`。
2. 检查此前依赖敏感词子串匹配的项目字段，使用 `ExtraSensitiveKeys` 添加准确名称。
3. 移除 `ErrContextAttrsPanic`、`ErrHandlerPanic` 引用；项目自行处理回调与输出 Handler 的故障。
4. 按包含组节点的新预算检查重要字段是否被截断；优先输出 service、request ID 等必要字段。
5. 在项目 Collector 中验证语言等级映射与字段落库；不需要恢复远程 Client。

## Collector与可靠性

推荐让JSON Handler写stdout，再由项目部署Vector等Collector。Collector必须配置有界磁盘buffer、重启恢复、批量写入、数据库故障重放、磁盘容量指标和明确的满载策略。数据库sink通常是at-least-once，记录可能重复；stdout写入成功也不等于数据库或本地持久buffer已经确认。

事务性权限、财务或安全审计应在业务事务内写审计表或transactional outbox，再异步投影到查询系统。普通日志字段不应被解释为合规级不可丢失承诺。

## 从`v0.2.0`迁移

`v0.3.0`是破坏性版本，删除了`Event`、`EventKind`、自定义`Level`、`Client`、`Emitter`、`Logger`、远程`slog.Handler`、Kafka/DLQ常量和audit入口。迁移步骤是：

1. 项目先部署并验证自己的Collector、持久buffer和数据库投影；
2. 使用标准库JSON Handler和`NewSanitizingHandler`替换远程Client；
3. 删除logging-service地址、token、后台队列和`Close`生命周期；
4. 排空旧客户端、service spool、Kafka lag和DLQ后再停旧链路；
5. 将强事务审计迁移到业务数据库或outbox。

仍运行旧HTTP/Kafka链路的项目应继续固定`v0.2.0`，不能只升级SDK而保留旧发送代码。
