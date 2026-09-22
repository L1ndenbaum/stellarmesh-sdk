# Go 基础 SDK

只依赖标准库，适用于 Go 1.24；提供环境解析、JSON 请求解码和 HTTP server 构造。

## 安装

示例适用于 `v0.5.0`；其他组件各自独立版本，参阅[发布矩阵](https://github.com/L1ndenbaum/stellarmesh-sdk/blob/dev/docs/release.md#当前制品矩阵)。

```sh
go get github.com/L1ndenbaum/stellarmesh-sdk/sdk/go@v0.5.0
```

## 最小完整示例

本例使用本地请求，不监听端口。Decode 负责关闭请求体，响应协议由调用方定义。 将源码作为外部包的 `example_test.go`，使用 `go test` 编译；只有带 `Output` 的示例会被执行。

<!-- example: sdk/go/http/jsonbody/example_test.go -->
```go
package jsonbody_test

import (
	"fmt"
	"net/http/httptest"
	"strings"

	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/http/jsonbody"
)

func ExampleDecode() {
	request := httptest.NewRequest("POST", "/items", strings.NewReader(`{"name":"示例"}`))
	response := httptest.NewRecorder()
	var input struct {
		Name string `json:"name"`
	}
	if err := jsonbody.Decode(response, request, &input, jsonbody.Options{DisallowUnknownFields: true}); err != nil {
		fmt.Println("请求无效", err)
		return
	}
	// Decode 已关闭请求体，响应协议由项目决定。
	fmt.Println(input.Name)
	// Output: 示例
}
```
<!-- /example -->

## 关键限制与深入指南

详细配置、错误与迁移见[接入指南](https://github.com/L1ndenbaum/stellarmesh-sdk/blob/dev/docs/sdk/go/README.md)。同时引入父 Module 与嵌套 Module 时，父 Module 需采用拆分完成后的版本，不能用长期本地 replace 掩盖 ambiguous import。源码布局与验证见[贡献指南](https://github.com/L1ndenbaum/stellarmesh-sdk/blob/dev/CONTRIBUTING.md)。
