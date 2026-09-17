package sessionauth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/gateway"
	"github.com/redis/go-redis/v9"
)

type fakeRedis struct {
	result interface{}
	err    error
	calls  int
}

func (f *fakeRedis) Eval(_ context.Context, _ string, _ []string, _ ...interface{}) *redis.Cmd {
	f.calls++
	return redis.NewCmdResult(f.result, f.err)
}
func TestStoreRejectsInvalidConfiguration(t *testing.T) {
	var nilClient *fakeRedis
	for _, config := range []StoreConfig{{}, {Client: nilClient, ProjectScope: "p"}, {Client: &fakeRedis{}}, {Client: &fakeRedis{}, ProjectScope: "p", MaxSessionsPerUser: -1}, {Client: &fakeRedis{}, ProjectScope: "p", KeySeparator: "{"}, {Client: redis.NewClusterClient(&redis.ClusterOptions{}), ProjectScope: "p"}} {
		if _, err := NewStore(config); err == nil {
			t.Fatal("accepted invalid config")
		}
	}
}
func TestStoreValidatesInputsAndResults(t *testing.T) {
	ctx := context.Background()
	client := &fakeRedis{result: []interface{}{"ok"}}
	store, err := NewStore(StoreConfig{Client: client, ProjectScope: "p"})
	if err != nil {
		t.Fatal(err)
	}
	for _, options := range []CreateOptions{{}, {Identity: gateway.Identity{UserID: "u"}}, {Identity: gateway.Identity{UserID: "u"}, TTL: time.Microsecond}, {Identity: gateway.Identity{UserID: "u", Attributes: map[string]any{"bad": make(chan int)}}, TTL: time.Hour}} {
		if _, err := store.Create(ctx, options); err == nil {
			t.Fatal("accepted invalid input")
		}
	}
	if client.calls != 0 {
		t.Fatal("invalid input reached Redis")
	}
	options := CreateOptions{Identity: gateway.Identity{UserID: "u"}, TTL: time.Hour}
	if _, err := store.Create(ctx, options); !errors.Is(err, ErrCorruptSession) {
		t.Fatalf("bad result: %v", err)
	}
	client.result = []interface{}{"collision"}
	client.calls = 0
	if _, err := store.Create(ctx, options); !errors.Is(err, ErrSessionCollision) || client.calls != 3 {
		t.Fatalf("collision: %v calls=%d", err, client.calls)
	}
	cause := errors.New("connection failed")
	client.err = cause
	if _, err := store.Create(ctx, options); !errors.Is(err, cause) {
		t.Fatal(err)
	}
	if _, err := store.ListByUser(ctx, ""); err == nil {
		t.Fatal("empty user")
	}
	if err := store.RevokeByUser(ctx, ""); err == nil {
		t.Fatal("empty user")
	}
	if _, _, err := store.Renew(ctx, "invalid", 0); err == nil {
		t.Fatal("invalid TTL")
	}
}
