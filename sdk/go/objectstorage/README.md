# Go Object Storage

适用于 Go 1.24，需要外部 S3／MinIO、项目凭据和既有 Bucket。

## 安装

示例适用于 `v0.1.0`；其他组件各自独立版本，参阅[发布矩阵](https://github.com/L1ndenbaum/stellarmesh-sdk/blob/dev/docs/release.md#当前制品矩阵)。

```sh
go get github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/objectstorage@v0.1.0
```

## 最小完整示例

本例只编译验证，执行前需 AWS 凭据链、example-documents Bucket 和 example.txt。SDK 不创建 Bucket；对象响应体由调用方关闭。 将源码作为外部包的 `example_test.go`，使用 `go test` 编译；只有带 `Output` 的示例会被执行。

<!-- example: sdk/go/objectstorage/s3store/example_test.go -->
```go
package s3store_test

import (
	"context"
	"io"
	"time"

	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/objectstorage"
	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/objectstorage/s3store"
)

// ExampleNew 只编译验证；执行前需 AWS 凭据链、既有 Bucket 与 example.txt。
func ExampleNew() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := s3store.New(ctx, s3store.Config{Region: "us-east-1", Namespace: objectstorage.Namespace{Bucket: "example-documents"}})
	if err != nil {
		panic(err)
	}
	object, err := client.Get(ctx, objectstorage.GetRequest{Object: objectstorage.ObjectRef{Key: "example.txt"}})
	if err != nil {
		panic(err)
	}
	defer object.Body.Close()
	if _, err := io.Copy(io.Discard, object.Body); err != nil {
		panic(err)
	}
	// SDK 没有 Client.Close；若注入自有 HTTP Transport，其空闲连接由业务管理。
}
```
<!-- /example -->

## 关键限制与深入指南

详细配置、错误与迁移见[接入指南](https://github.com/L1ndenbaum/stellarmesh-sdk/blob/dev/docs/sdk/go/object-storage.md)。同时引入父 Module 与嵌套 Module 时，父 Module 需采用拆分完成后的版本，不能用长期本地 replace 掩盖 ambiguous import。源码布局与验证见[贡献指南](https://github.com/L1ndenbaum/stellarmesh-sdk/blob/dev/CONTRIBUTING.md)。
