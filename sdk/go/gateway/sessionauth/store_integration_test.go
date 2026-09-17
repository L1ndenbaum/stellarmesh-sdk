package sessionauth_test

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/gateway"
	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/gateway/sessionauth"
	"github.com/redis/go-redis/v9"
)

func integrationStore(t *testing.T, limit int) (*sessionauth.Store, *redis.Client, string) {
	t.Helper()
	addr := os.Getenv("STELLARMESH_SESSION_REDIS_ADDR")
	if addr == "" {
		t.Skip("需要隔离 Redis：运行 make integration-session")
	}
	client := redis.NewClient(&redis.Options{Addr: addr, MaxRetries: -1})
	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	scope := "session-test-" + rand.Text()
	store, err := sessionauth.NewStore(sessionauth.StoreConfig{Client: client, ProjectScope: scope, MaxSessionsPerUser: limit})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// 只清理本用例随机命名空间，不清空共享 Redis 数据库。
		var cursor uint64
		for {
			keys, next, err := client.Scan(ctx, cursor, scope+":*", 100).Result()
			if err != nil {
				break
			}
			if len(keys) > 0 {
				_ = client.Del(ctx, keys...).Err()
			}
			cursor = next
			if cursor == 0 {
				break
			}
		}
		_ = client.Close()
	})
	return store, client, scope
}
func createSession(t *testing.T, store *sessionauth.Store, user string, ttl time.Duration) sessionauth.Session {
	t.Helper()
	s, err := store.Create(context.Background(), sessionauth.CreateOptions{Identity: gateway.Identity{UserID: user, Roles: []string{}, Attributes: map[string]any{"number": int64(9007199254740993)}}, TTL: ttl})
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func TestRedisSessionLifecycle(t *testing.T) {
	store, client, scope := integrationStore(t, 0)
	ctx := context.Background()
	s := createSession(t, store, "u", time.Minute)
	got, found, err := store.Lookup(ctx, s.ID)
	if err != nil || !found || got.ID != s.ID || got.Identity.Roles == nil {
		t.Fatalf("lookup=%+v %v %v", got, found, err)
	}
	renewed, found, err := store.Renew(ctx, s.ID, 2*time.Minute)
	if err != nil || !found || !renewed.CreatedAt.Equal(s.CreatedAt) || !renewed.ExpiresAt.After(s.ExpiresAt) {
		t.Fatalf("renew=%+v %v", renewed, err)
	}
	keys, _ := sessionauth.NewKeyBuilder(sessionauth.KeyConfig{})
	index, _ := keys.UserSessionsKey(scope, "u")
	key, _ := keys.SessionKey(scope, s.ID)
	if ttl := client.PTTL(ctx, index).Val(); ttl < time.Minute || ttl > 2*time.Minute {
		t.Fatal(ttl)
	}
	// 身份 JSON 不经过 Lua 数字转换；原始整数保存在 Redis 中。
	if raw := client.Get(ctx, key).Val(); !strings.Contains(raw, "9007199254740993") {
		t.Fatal("identity JSON lost integer precision")
	}
	list, err := store.ListByUser(ctx, "u")
	if err != nil || len(list) != 1 {
		t.Fatalf("list=%v err=%v", list, err)
	}
	if err := store.Revoke(ctx, s.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.Revoke(ctx, s.ID); err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.Renew(ctx, s.ID, time.Minute); err != nil || found {
		t.Fatalf("revived session %v %v", found, err)
	}
	if count := client.Exists(ctx, index, key).Val(); count != 0 {
		t.Fatal("revoke left keys")
	}
}

func TestRedisExpirationAndIndexCleanup(t *testing.T) {
	store, client, scope := integrationStore(t, 0)
	ctx := context.Background()
	short := createSession(t, store, "u", 30*time.Millisecond)
	long := createSession(t, store, "u", time.Minute)
	time.Sleep(60 * time.Millisecond)
	if _, found, err := store.Lookup(ctx, short.ID); found || err != nil {
		t.Fatalf("expired %v %v", found, err)
	}
	list, err := store.ListByUser(ctx, "u")
	if err != nil || len(list) != 1 || list[0].ID != long.ID {
		t.Fatalf("list=%v err=%v", list, err)
	}
	keys, _ := sessionauth.NewKeyBuilder(sessionauth.KeyConfig{})
	index, _ := keys.UserSessionsKey(scope, "u")
	if client.ZCard(ctx, index).Val() != 1 {
		t.Fatal("stale member remained")
	}
	if _, _, err := store.Renew(ctx, long.ID, 30*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	time.Sleep(60 * time.Millisecond)
	if client.Exists(ctx, index).Val() != 0 {
		t.Fatal("index outlived last session")
	}
}
func TestRedisDefaultLimitEvictsOldestNotLeastRecentlyRenewed(t *testing.T) {
	store, _, _ := integrationStore(t, 0)
	ctx := context.Background()
	oldest := createSession(t, store, "u", time.Minute)
	time.Sleep(2 * time.Millisecond)
	for i := 0; i < 9; i++ {
		createSession(t, store, "u", time.Minute)
	}
	if _, _, err := store.Renew(ctx, oldest.ID, time.Hour); err != nil {
		t.Fatal(err)
	}
	newest := createSession(t, store, "u", time.Minute)
	if _, found, err := store.Lookup(ctx, oldest.ID); err != nil || found {
		t.Fatalf("oldest not evicted: %v %v", found, err)
	}
	list, err := store.ListByUser(ctx, "u")
	if err != nil || len(list) != 10 {
		t.Fatalf("len=%d err=%v", len(list), err)
	}
	if _, found, err := store.Lookup(ctx, newest.ID); err != nil || !found {
		t.Fatal("new session evicted", err)
	}
	if !sort.SliceIsSorted(list, func(i, j int) bool {
		if list[i].CreatedAt.Equal(list[j].CreatedAt) {
			return list[i].ID < list[j].ID
		}
		return list[i].CreatedAt.Before(list[j].CreatedAt)
	}) {
		t.Fatal("unordered sessions")
	}
}
func TestRedisScopeCustomKeysAndReducedLimit(t *testing.T) {
	store, client, scope := integrationStore(t, 0)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		createSession(t, store, "u", time.Minute)
	}
	reduced, err := sessionauth.NewStore(sessionauth.StoreConfig{Client: client, ProjectScope: scope, MaxSessionsPerUser: 2})
	if err != nil {
		t.Fatal(err)
	}
	createSession(t, reduced, "u", time.Minute)
	list, err := reduced.ListByUser(ctx, "u")
	if err != nil || len(list) != 2 {
		t.Fatal(len(list), err)
	}
	other, err := sessionauth.NewStore(sessionauth.StoreConfig{Client: client, ProjectScope: scope + ":other", KeySeparator: "-_"})
	if err != nil {
		t.Fatal(err)
	}
	isolated := createSession(t, other, "用 户:{%}", time.Minute)
	t.Cleanup(func() { _ = other.RevokeByUser(ctx, "用 户:{%}") })
	if _, found, err := store.Lookup(ctx, isolated.ID); err != nil || found {
		t.Fatal("crossed project boundary", err)
	}
	if list, err := other.ListByUser(ctx, "用 户:{%}"); err != nil || len(list) != 1 {
		t.Fatal(list, err)
	}
	if err := store.RevokeByUser(ctx, "u"); err != nil {
		t.Fatal(err)
	}
	if _, found, err := other.Lookup(ctx, isolated.ID); err != nil || !found {
		t.Fatal("revoked other project", err)
	}
}
func TestRedisConcurrentCreateRenewAndRevocation(t *testing.T) {
	store, _, _ := integrationStore(t, 0)
	ctx := context.Background()
	var wg sync.WaitGroup
	failures := make(chan error, 80)
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := store.Create(ctx, sessionauth.CreateOptions{Identity: gateway.Identity{UserID: "u"}, TTL: time.Minute})
			if err != nil {
				failures <- err
				return
			}
			if _, _, err := store.Renew(ctx, s.ID, time.Minute); err != nil {
				failures <- err
			}
		}()
	}
	wg.Wait()
	list, err := store.ListByUser(ctx, "u")
	if err != nil || len(list) != 10 {
		t.Fatal(len(list), err)
	}
	for _, s := range list {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			_, _, err := store.Renew(ctx, id, time.Minute)
			if err != nil {
				failures <- err
			}
		}(s.ID)
	}
	if err := store.RevokeByUser(ctx, "u"); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	for _, s := range list {
		if _, found, err := store.Lookup(ctx, s.ID); err != nil || found {
			t.Fatal("revived by concurrent renew", err)
		}
	}
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := store.Create(ctx, sessionauth.CreateOptions{Identity: gateway.Identity{UserID: "u"}, TTL: time.Minute})
			if err != nil {
				failures <- err
			}
		}()
	}
	if err := store.RevokeByUser(ctx, "u"); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if err := store.RevokeByUser(ctx, "u"); err != nil {
		t.Fatal(err)
	}
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	if list, err := store.ListByUser(ctx, "u"); err != nil || len(list) != 0 {
		t.Fatal(list, err)
	}
	fresh := createSession(t, store, "u", time.Minute)
	if _, found, err := store.Lookup(ctx, fresh.ID); err != nil || !found {
		t.Fatal("new login rejected", err)
	}
	// 单会话撤销也不能被并发续期复活。
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _, err := store.Renew(ctx, fresh.ID, time.Minute)
		if err != nil {
			t.Error(err)
		}
	}()
	if err := store.Revoke(ctx, fresh.ID); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if _, found, err := store.Lookup(ctx, fresh.ID); err != nil || found {
		t.Fatal("single session revived", err)
	}
}
func TestRedisCorruptStateFailsBeforeEviction(t *testing.T) {
	store, client, scope := integrationStore(t, 1)
	ctx := context.Background()
	s := createSession(t, store, "u", time.Minute)
	keys, _ := sessionauth.NewKeyBuilder(sessionauth.KeyConfig{})
	key, _ := keys.SessionKey(scope, s.ID)
	if err := client.Set(ctx, key, "{broken", time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(ctx, sessionauth.CreateOptions{Identity: gateway.Identity{UserID: "u"}, TTL: time.Minute}); !errors.Is(err, sessionauth.ErrCorruptSession) {
		t.Fatal(err)
	}
	if value := client.Get(ctx, key).Val(); value != "{broken" {
		t.Fatal("mutated before validation")
	}
	if _, _, err := store.Lookup(ctx, s.ID); !errors.Is(err, sessionauth.ErrCorruptSession) {
		t.Fatal(err)
	}
}
func TestRedisCookieAuthenticationAndKick(t *testing.T) {
	store, client, _ := integrationStore(t, 0)
	ctx := context.Background()
	s := createSession(t, store, "user-1", time.Minute)
	auth, err := sessionauth.NewAuthenticator(store)
	if err != nil {
		t.Fatal(err)
	}
	cookie, err := gateway.CookieCredential("sid")
	if err != nil {
		t.Fatal(err)
	}
	handler, err := gateway.New(gateway.WithoutAccessLog(), gateway.WithRoutes(gateway.Route{Name: "api", Match: gateway.RouteMatch{ExactPath: "/"}, Upstream: "app"}), gateway.WithAuthenticator(auth, cookie), gateway.WithUpstreamResolver(gateway.UpstreamResolverFunc(func(gateway.Route) (http.Handler, error) {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-User-ID") != "user-1" {
				t.Error("identity missing")
			}
			w.WriteHeader(204)
		}), nil
	})))
	if err != nil {
		t.Fatal(err)
	}
	request := func(id string) int {
		r := httptest.NewRequest("GET", "/", nil)
		r.AddCookie(&http.Cookie{Name: "sid", Value: id})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Code
	}
	if code := request(s.ID); code != 204 {
		t.Fatal(code)
	}
	if err := store.RevokeByUser(ctx, "user-1"); err != nil {
		t.Fatal(err)
	}
	if code := request(s.ID); code != 401 {
		t.Fatal(code)
	}
	fresh := createSession(t, store, "user-1", time.Minute)
	if err := store.RevokeByUser(ctx, "user-1"); err != nil {
		t.Fatal(err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if code := request(fresh.ID); code != 503 {
		t.Fatal(fmt.Sprintf("Redis failure status=%d", code))
	}
}
