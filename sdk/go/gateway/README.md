# Go Gateway

适用于 Go 1.24，项目声明路由、上游、认证与策略；SDK 固定安全顺序。

## 安装

示例适用于 `v0.5.1`；其他组件各自独立版本，参阅[发布矩阵](https://github.com/L1ndenbaum/stellarmesh-sdk/blob/dev/docs/release.md#当前制品矩阵)。

```sh
go get github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/gateway@v0.5.1
```

## 最小完整示例

本例启动并关闭两个本地测试服务器。真实部署需自行配置安全策略；Session 与凭证提取扩展要求 Gateway `v0.4.0` 或更新版本。 将源码作为外部包的 `example_test.go`，使用 `go test` 编译；只有带 `Output` 的示例会被执行。

<!-- example: sdk/go/gateway/example_test.go -->
```go
package gateway_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"

	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/gateway"
)

func ExampleNew() {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	handler, err := gateway.New(
		gateway.WithRoutes(gateway.Route{Name: "public", Match: gateway.RouteMatch{ExactPath: "/items"}, Upstream: "api", Access: gateway.AccessPublic}),
		gateway.WithUpstreams(gateway.Upstream{Name: "api", URL: upstream.URL}),
		gateway.WithoutAccessLog(),
	)
	if err != nil {
		panic(err)
	}
	proxy := httptest.NewServer(handler)
	defer proxy.Close()
	response, err := proxy.Client().Get(proxy.URL + "/items")
	if err != nil {
		panic(err)
	}
	defer response.Body.Close()
	if _, err := io.Copy(io.Discard, response.Body); err != nil {
		panic(err)
	}
	fmt.Println(response.StatusCode)
	// Output: 204
}
```
<!-- /example -->

## 关键限制与深入指南

`v0.5.0` 默认重新生成请求 ID；只有显式配置 `RequestIDConfig.TrustIncoming: true` 才沿用单个合法传入值，来源可信性由项目保证。升级前参阅[请求 ID 迁移](https://github.com/L1ndenbaum/stellarmesh-sdk/blob/dev/docs/sdk/go/gateway-migration.md#从-v040-升级到-v050)。

`v0.5.1` 补齐内置反向代理的请求 ID 传递和响应去重，使用默认及自定义请求头时均复用入口选定的值。

详细配置、错误与迁移见[接入指南](https://github.com/L1ndenbaum/stellarmesh-sdk/blob/dev/docs/sdk/go/gateway.md)。同时引入父 Module 与嵌套 Module 时，父 Module 需采用拆分完成后的版本，不能用长期本地 replace 掩盖 ambiguous import。源码布局与验证见[贡献指南](https://github.com/L1ndenbaum/stellarmesh-sdk/blob/dev/CONTRIBUTING.md)。
