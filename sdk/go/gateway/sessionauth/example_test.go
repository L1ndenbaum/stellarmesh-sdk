package sessionauth_test

import (
	"context"
	"fmt"
	"time"

	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/gateway"
	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/gateway/sessionauth"
	"github.com/redis/go-redis/v9"
)

func ExampleKeyBuilder_SessionKey() {
	keys, err := sessionauth.NewKeyBuilder(sessionauth.KeyConfig{})
	if err != nil {
		panic(err)
	}
	key, err := keys.SessionKey("kgraph", "example-id")
	if err != nil {
		panic(err)
	}
	fmt.Println(key)
	// Output: kgraph:session:example-id
}

// ExampleNewStore 只编译验证；执行前需本地单实例 Redis，SDK 不安装 Redis。
func ExampleNewStore() {
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:6379"})
	defer func() {
		if err := client.Close(); err != nil {
			panic(err)
		}
	}()
	store, err := sessionauth.NewStore(sessionauth.StoreConfig{Client: client, ProjectScope: "docs-example"})
	if err != nil {
		panic(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session, err := store.Create(ctx, sessionauth.CreateOptions{Identity: gateway.Identity{UserID: "example-user"}, TTL: time.Minute})
	if err != nil {
		panic(err)
	}
	// 登录 HTTP 层负责安全 Cookie；不要打印作为凭证的 session.ID。
	if err := store.Revoke(ctx, session.ID); err != nil {
		panic(err)
	}
}
