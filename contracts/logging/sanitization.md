# 结构化日志字段清洗约定

本约定对应待发布的 Go/Python Logging `0.4.0`，只约束日志字段清洗，不定义远程 Event、完整日志 Schema、Collector 或数据库协议。共同可表达的行为样例位于 [sanitization-cases.json](sanitization-cases.json)，由 Python 测试和独立 Go 消费者共同验证。历史 `v1/`、`v2/` 继续冻结。

## 字段与预算

消息默认最多 `16KiB`，字符串值默认最多 `16KiB`，项目字段节点默认最多 `64`，深度默认最多 `8`。字符串按 UTF-8 字节截断，并在上限内以 `[TRUNCATED]` 结尾。

每个具名字段、具名组和序列元素消耗一个节点。顶层字段深度为 `1`，进入具名组或容器时深度增加；超过深度的值输出 `[TRUNCATED]`。敏感字段始终优先输出 `[REDACTED]`，不读取内部值。固定字段、请求上下文字段、当前记录和嵌套容器共享同一份预算，预算耗尽后忽略后续内容。

Go 的 `WithGroup` 和显式 `slog.Group` 使用相同规则；同一层同名组路径合并，只计一次组节点。匿名内联组不额外计节点或深度；空组遵循标准库的省略规则，已经消耗的路径预算不返还。Go 按固定字段、上下文字段、当前记录的顺序处理；Python 按固定字段、当前记录的顺序处理。Go map 的遍历顺序不保证稳定，业务不能依赖超限 map 中具体保留哪些字段。

标准日志时间、级别、消息和可选来源、异常结构不占项目字段预算。上述限制不是整条 JSON 的最大字节数承诺。SDK 不修改调用方的容器；固定字段只复制顶层结构，业务不能在输出时并发修改其嵌套值。

## 脱敏规则

字段名只保留 Unicode 字母和十进制数字，再转小写后精确匹配。内置规范化名称为：

```text
apikey authorization clientsecret cookie credential jwt password
privatekey secret session token accesstoken refreshtoken idtoken
sessiontoken csrftoken xsrftoken setcookie
```

因此 `apiKey`、`API-KEY`、`api_key` 均脱敏，`access_token` 等明确凭据字段也脱敏；`token_count`、`prompt_tokens`、`session_id` 不会因为包含某个敏感词而被遮盖。项目可以通过 Go `ExtraSensitiveKeys` 或 Python `extra_sensitive_keys` 添加精确名称，不能关闭内置集合。

规则只处理字段名，不搜索消息、错误文本或异常堆栈中的凭据。项目自己的 `db_password` 等名称应显式配置；从 `0.3.0` 升级时必须检查此前依赖子串匹配的字段。

## 支持类型

| 语言 | 支持值 | 不再隐式展开 |
| --- | --- | --- |
| Go | 标准标量及同底层类型的命名标量、`time.Time`、`time.Duration`、`error`、`slog.Value`/`LogValuer`、字符串键 map、slice、array | 任意业务 struct、pointer、`MarshalJSON`、`String()` |
| Python | 标准标量、内置 dict/list/tuple、`date`/`datetime`、UUID、Enum、异常、bytes | dataclass、自定义 Mapping/Sequence、容器子类、任意业务对象 |

Go `error` 和 `LogValuer` 是显式转换接口，即使由业务 struct 或 pointer 实现也保留支持。Go 的命名标量只按底层值输出，不调用其自定义序列化方法。Python 字典的 key 必须是内置字符串，不隐式调用任意 key 的 `__str__`。

字节值输出 `<bytes:长度>`；不支持的值、非有限浮点数和错误文本转换失败输出 `[UNSERIALIZABLE]`。Python 检出循环容器时输出 `[UNSERIALIZABLE]`；Go 的循环容器由深度和节点预算终止。复杂业务对象应先转换为少量明确字段，Go 也可实现 `LogValuer`。

## 错误与输出责任

Go 不再捕获项目 `ContextAttrs` 或下游 Handler 的 panic，也不再导出 `ErrContextAttrsPanic`、`ErrHandlerPanic`。下游 `Handle` 的普通错误原样返回，`Enabled` 保持下游语义。标准 `slog.Logger` 不向业务日志调用者返回 Handler 错误，项目需自行安排输出错误观测。

字段层保留必要降级：Go 错误文本转换 panic 转为占位符，`LogValuer` 使用标准库 Resolve 语义；Python 消息格式化和异常文本转换失败使用占位符。SDK 不承诺任意项目回调、输出 stream 或自定义对象永远不会抛异常。

等级和输出目标由标准库拥有。Go 的 `WARN`、Python 的 `WARNING`/`CRITICAL` 等语言差异由项目 Collector 明确映射，SDK 不维护第二套等级。
