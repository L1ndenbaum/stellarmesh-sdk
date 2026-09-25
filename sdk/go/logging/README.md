# Go Logging

适用于 Go 1.24，装饰标准库 slog.Handler，无第三方运行依赖。

## 安装

示例适用于 `v0.4.1`；其他组件各自独立版本，参阅[发布矩阵](https://github.com/L1ndenbaum/stellarmesh-sdk/blob/dev/docs/release.md#当前制品矩阵)。

```sh
go get github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging@v0.4.1
```

## 最小完整示例

本例执行实际 JSON 输出与脱敏断言。应用拥有输出流、级别和持久化策略；消息正文不会自动扫描秘密。 将源码作为外部包的 `example_test.go`，使用 `go test` 编译；只有带 `Output` 的示例会被执行。

<!-- example: sdk/go/logging/example_test.go -->
```go
package logging_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging"
)

func ExampleNewSanitizingHandler() {
	var output bytes.Buffer
	handler, err := logging.NewSanitizingHandler(slog.NewJSONHandler(&output, nil), logging.HandlerOptions{})
	if err != nil {
		panic(err)
	}
	logger := slog.New(handler)
	logger.Info("示例请求", "password", "example-secret")
	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		panic(err)
	}
	// Buffer 无关闭操作；换成文件时由应用负责关闭。
	fmt.Println(record["password"])
	// Output: [REDACTED]
}
```
<!-- /example -->

## 关键限制与深入指南

详细配置、错误与迁移见[接入指南](https://github.com/L1ndenbaum/stellarmesh-sdk/blob/dev/docs/sdk/go/logging.md)。同时引入父 Module 与嵌套 Module 时，父 Module 需采用拆分完成后的版本，不能用长期本地 replace 掩盖 ambiguous import。源码布局与验证见[贡献指南](https://github.com/L1ndenbaum/stellarmesh-sdk/blob/dev/CONTRIBUTING.md)。
