package sessionauth

import (
	"context"
	"crypto/rand"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/gateway"
	"github.com/redis/go-redis/v9"
)

func TestRedisEvictsInCreationOrderWithinOneMillisecond(t *testing.T) {
	addr := os.Getenv("STELLARMESH_SESSION_REDIS_ADDR")
	if addr == "" {
		t.Skip("需要隔离 Redis：运行 make integration-session")
	}
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	for _, tc := range []struct {
		name        string
		legacyCount int
	}{
		{"new", 0},
		{"legacy_then_new", 1},
		{"two_legacy", 2},
	} {
		t.Run(tc.name, func(t *testing.T) { checkSessionCreationOrder(t, client, tc.legacyCount) })
	}
}

func checkSessionCreationOrder(t *testing.T, client *redis.Client, legacyCount int) {
	t.Helper()
	scope := "session-test-" + rand.Text()
	store, err := NewStore(StoreConfig{Client: client, ProjectScope: scope, MaxSessionsPerUser: 2})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	t.Cleanup(func() { _ = store.RevokeByUser(ctx, "u") })

	// Redis 事务内共享时钟快照。固定两个合法 ID，让字典序与脚本执行顺序相反。
	firstID, secondID := strings.Repeat("z", 42)+"A", strings.Repeat("A", 43)
	tx := client.TxPipeline()
	queued := *store
	queued.client = tx
	for i, id := range []string{firstID, secondID} {
		if i < legacyCount {
			key, err := store.keys.SessionKey(scope, id)
			if err != nil {
				t.Fatal(err)
			}
			index, err := store.keys.UserSessionsKey(scope, "u")
			if err != nil {
				t.Fatal(err)
			}
			tx.Eval(ctx, seedLegacySession, []string{key, index}, id)
		} else {
			queued.eval(ctx, "create", id, "u", time.Minute, `{"UserID":"u"}`)
		}
	}
	if _, err := tx.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	before, err := store.ListByUser(ctx, "u")
	if err != nil || len(before) != 2 || !before[0].CreatedAt.Equal(before[1].CreatedAt) {
		t.Fatalf("expected two sessions created in the same millisecond, count=%d err=%v", len(before), err)
	}
	// 两条旧记录没有保存毫秒内的先后，只能验证升级前已有的 ID 顺序。
	if legacyCount == 2 {
		firstID, secondID = secondID, firstID
	}
	if before[0].ID != firstID || before[1].ID != secondID {
		t.Error("sessions with the same timestamp listed out of creation order")
	}
	for _, id := range []string{secondID, firstID} {
		if _, found, err := store.Renew(ctx, id, time.Hour); err != nil || !found {
			t.Fatalf("renew session: found=%v err=%v", found, err)
		}
	}
	third, err := store.Create(ctx, CreateOptions{Identity: gateway.Identity{UserID: "u"}, TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	after, err := store.ListByUser(ctx, "u")
	if err != nil || len(after) != 2 || after[0].ID != secondID || after[1].ID != third.ID {
		t.Fatalf("expected the second and third sessions to survive in order, count=%d err=%v", len(after), err)
	}
	if _, found, err := store.Lookup(ctx, firstID); err != nil || found {
		t.Fatalf("first session after eviction: found=%v err=%v", found, err)
	}
}

// 固定旧版记录和索引的格式，避免用待测的 Create 生成兼容性样本。
const seedLegacySession = `
local clock = redis.call('TIME')
local now = tonumber(clock[1]) * 1000 + math.floor(tonumber(clock[2]) / 1000)
local identity = '{"UserID":"u"}'
local row = {version=1, id=ARGV[1], user_id='u', identity=identity, identity_hash=redis.sha1hex(identity),
             created_at=now, expires_at=now+60000}
redis.call('SET', KEYS[1], cjson.encode(row), 'PX', 60000)
redis.call('ZADD', KEYS[2], now, ARGV[1])
return redis.call('PEXPIREAT', KEYS[2], now+60000)
`
