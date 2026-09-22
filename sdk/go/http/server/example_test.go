package server_test

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/http/server"
)

func ExampleNew() {
	handler := http.NewServeMux()
	srv := server.New(server.Config{Addr: "127.0.0.1:8080", ReadHeaderTimeout: 5 * time.Second}, handler)
	// 示例只验证构造与关闭；实际应用负责 ListenAndServe 和退出信号。
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		panic(err)
	}
	fmt.Println(srv.ReadHeaderTimeout)
	// Output: 5s
}
