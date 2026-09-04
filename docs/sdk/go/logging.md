# Go Logging SDK 接入教程

本教程对应独立Module：

```text
github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging
```

`v0.3.0`要求Go 1.24及以上，只依赖标准库。它装饰项目已有的`slog.Handler`，提供脱敏和有界化；它不创建Logger、不选择stdout/stderr、不实现远程Client，也不定义项目字段或数据库Schema。

## 安装

```sh
go get github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging@v0.3.0
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

`ContextAttrs`只是一条注入接口，不规定context key或字段名。回调和下游Handler的panic会被转换为稳定错误，不会从标准库Logger调用传播到业务流程；项目如果需要统计Handler失败，应在自己控制的下游Handler或观测层记录指标，不能假定一条`slog.Info`返回代表日志已经落盘。

## 安全边界

默认限制如下：

| 配置 | 默认值 | 说明 |
| --- | ---: | --- |
| `MaxMessageBytes` | `16KiB` | message UTF-8字节上限 |
| `MaxStringBytes` | `16KiB` | 字符串字段UTF-8字节上限 |
| `MaxAttributes` | `64` | 单条记录的项目属性预算 |
| `MaxDepth` | `8` | 嵌套结构深度 |

内置敏感字段覆盖password、secret、token、authorization、cookie、API key、client secret、private key等常见变体。匹配前会把key转小写并移除非字母数字字符，因此`apiKey`、`api_key`、`API-KEY`使用同一规则。`ExtraSensitiveKeys`只能扩展默认集合，不能关闭默认脱敏。

敏感值输出`[REDACTED]`，不可安全序列化的值输出`[UNSERIALIZABLE]`，超限字符串以`[TRUNCATED]`结尾。`error`值转换为受限的`Error()`文本，非有限浮点数不会直接进入JSON。装饰器支持`WithAttrs`、`WithGroup`、`LogValuer`和嵌套group，且不会修改调用方传入的attribute slice或map。

SDK只按key脱敏，不扫描任意message或错误文本中的Secret。项目仍应避免把Authorization、Cookie、请求体、响应体和凭据拼入message。

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
