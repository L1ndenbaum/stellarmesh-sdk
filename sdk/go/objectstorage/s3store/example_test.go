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
