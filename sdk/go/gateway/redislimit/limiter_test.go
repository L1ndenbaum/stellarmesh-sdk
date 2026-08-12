package redislimit

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/gateway"
	"github.com/redis/go-redis/v9"
)

func TestLimiterUsesAtomicScriptAndParsesDecision(t *testing.T) {
	runner := &fakeRunner{result: []interface{}{int64(0), "0.25", int64(750)}}
	limiter, err := New(Config{
		Client: runner, Scope: gateway.RateLimitScopeClientIP, KeyPrefix: "gateway:rate",
		RatePerSecond: 2, Burst: 4, Now: func() time.Time { return time.UnixMilli(1234) },
	})
	if err != nil {
		t.Fatal(err)
	}
	decision, err := limiter.Allow(context.Background(), gateway.RateLimitRequest{Scope: gateway.RateLimitScopeClientIP, Key: "203.0.113.1"})
	if err != nil || decision.Allowed || decision.Remaining != 0 || decision.RetryAfter != 750*time.Millisecond {
		t.Fatalf("Allow() = %#v, %v", decision, err)
	}
	if !strings.Contains(runner.script, "redis.call(\"HSET\"") || len(runner.keys) != 1 || strings.Contains(runner.keys[0], "203.0.113.1") {
		t.Fatalf("script = %q, keys = %#v", runner.script, runner.keys)
	}
	if len(runner.args) != 4 || runner.args[0] != int64(1234) || runner.args[1] != float64(2) || runner.args[2] != int64(4) {
		t.Fatalf("args = %#v", runner.args)
	}
}

func TestLimiterReturnsRedisFailure(t *testing.T) {
	limiter, err := New(Config{
		Client: &fakeRunner{err: errors.New("redis unavailable")}, Scope: gateway.RateLimitScopeUserID,
		KeyPrefix: "gateway:rate", RatePerSecond: 1, Burst: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := limiter.Allow(context.Background(), gateway.RateLimitRequest{Scope: gateway.RateLimitScopeUserID, Key: "user-1"}); err == nil {
		t.Fatal("Allow() ignored Redis failure")
	}
}

func TestLimiterRejectsScopeMismatch(t *testing.T) {
	limiter, err := New(Config{
		Client: &fakeRunner{}, Scope: gateway.RateLimitScopeUserID,
		KeyPrefix: "gateway:rate", RatePerSecond: 1, Burst: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := limiter.Allow(context.Background(), gateway.RateLimitRequest{Scope: gateway.RateLimitScopeClientIP, Key: "user-1"}); err == nil {
		t.Fatal("Allow() accepted mismatched scope")
	}
}

func TestNewRejectsDisabledOrUnsafeConfiguration(t *testing.T) {
	var nilRunner *fakeRunner
	tests := []Config{
		{},
		{Client: nilRunner, Scope: gateway.RateLimitScopeClientIP, KeyPrefix: "gateway", RatePerSecond: 1, Burst: 1},
		{Client: &fakeRunner{}, Scope: "unknown", KeyPrefix: "gateway", RatePerSecond: 1, Burst: 1},
		{Client: &fakeRunner{}, Scope: gateway.RateLimitScopeClientIP, RatePerSecond: 1, Burst: 1},
		{Client: &fakeRunner{}, Scope: gateway.RateLimitScopeClientIP, KeyPrefix: "bad prefix", RatePerSecond: 1, Burst: 1},
		{Client: &fakeRunner{}, Scope: gateway.RateLimitScopeClientIP, KeyPrefix: "gateway", RatePerSecond: 0, Burst: 1},
		{Client: &fakeRunner{}, Scope: gateway.RateLimitScopeClientIP, KeyPrefix: "gateway", RatePerSecond: 1, Burst: 0},
	}
	for _, config := range tests {
		if _, err := New(config); err == nil {
			t.Fatalf("New() accepted %#v", config)
		}
	}
}

type fakeRunner struct {
	result any
	err    error
	script string
	keys   []string
	args   []interface{}
}

func (runner *fakeRunner) Eval(_ context.Context, script string, keys []string, args ...interface{}) *redis.Cmd {
	runner.script = script
	runner.keys = append([]string(nil), keys...)
	runner.args = append([]interface{}(nil), args...)
	return redis.NewCmdResult(runner.result, runner.err)
}
