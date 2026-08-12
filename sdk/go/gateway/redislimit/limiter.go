// Package redislimit 提供原子的 Redis 令牌桶网关限流组件。
package redislimit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/gateway"
	"github.com/redis/go-redis/v9"
)

const tokenBucketScript = `
local key = KEYS[1]
local now_ms = tonumber(ARGV[1])
local rate = tonumber(ARGV[2])
local burst = tonumber(ARGV[3])
local ttl_ms = tonumber(ARGV[4])

local tokens = tonumber(redis.call("HGET", key, "tokens"))
local updated_at_ms = tonumber(redis.call("HGET", key, "updated_at_ms"))
if tokens == nil or updated_at_ms == nil then
  tokens = burst
  updated_at_ms = now_ms
end

local elapsed_ms = math.max(0, now_ms - updated_at_ms)
tokens = math.min(burst, tokens + ((elapsed_ms / 1000) * rate))

local allowed = 0
local retry_ms = 0
if tokens >= 1 then
  allowed = 1
  tokens = tokens - 1
else
  retry_ms = math.ceil(((1 - tokens) / rate) * 1000)
end

redis.call("HSET", key, "tokens", tokens, "updated_at_ms", now_ms)
redis.call("PEXPIRE", key, ttl_ms)
return {allowed, tostring(tokens), retry_ms}
`

// ScriptRunner 是 Redis Client、ClusterClient 与测试替身共同需要的最小接口。
type ScriptRunner interface {
	Eval(context.Context, string, []string, ...interface{}) *redis.Cmd
}

// Config 配置一个固定作用域和速率的 Redis 令牌桶。
type Config struct {
	Client        ScriptRunner
	Scope         gateway.RateLimitScope
	KeyPrefix     string
	RatePerSecond float64
	Burst         int64
	Now           func() time.Time
}

// Limiter 使用单条 Lua 脚本原子补充并消费令牌。
type Limiter struct {
	client        ScriptRunner
	scope         gateway.RateLimitScope
	keyPrefix     string
	ratePerSecond float64
	burst         int64
	ttl           time.Duration
	now           func() time.Time
}

// New 拒绝隐式禁用值并构造 Redis 限流组件。
func New(config Config) (*Limiter, error) {
	if isNilScriptRunner(config.Client) {
		return nil, errors.New("Redis rate limiter client is required")
	}
	if !validScope(config.Scope) {
		return nil, errors.New("Redis rate limiter scope is invalid")
	}
	prefix := strings.TrimSpace(config.KeyPrefix)
	if prefix == "" || len(prefix) > 128 || strings.ContainsAny(prefix, "\r\n\t ") {
		return nil, errors.New("Redis rate limiter key prefix is invalid")
	}
	if config.RatePerSecond <= 0 || math.IsNaN(config.RatePerSecond) || math.IsInf(config.RatePerSecond, 0) {
		return nil, errors.New("Redis rate limiter rate must be finite and positive")
	}
	if config.Burst <= 0 {
		return nil, errors.New("Redis rate limiter burst must be positive")
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	ttlSeconds := math.Max(1, 2*float64(config.Burst)/config.RatePerSecond)
	if math.IsInf(ttlSeconds, 0) || ttlSeconds > float64(math.MaxInt64)/float64(time.Second) {
		return nil, errors.New("Redis rate limiter token refill duration is too large")
	}
	return &Limiter{
		client: config.Client, scope: config.Scope, keyPrefix: prefix,
		ratePerSecond: config.RatePerSecond, burst: config.Burst,
		ttl: time.Duration(ttlSeconds * float64(time.Second)), now: now,
	}, nil
}

// Allow 执行一次原子限流决策；Redis 异常直接返回给 fail-close 流水线。
func (limiter *Limiter) Allow(ctx context.Context, request gateway.RateLimitRequest) (gateway.RateLimitDecision, error) {
	if request.Scope != limiter.scope {
		return gateway.RateLimitDecision{}, errors.New("Redis rate limiter scope mismatch")
	}
	if strings.TrimSpace(request.Key) == "" {
		return gateway.RateLimitDecision{}, errors.New("Redis rate limiter key is empty")
	}
	result, err := limiter.client.Eval(
		ctx,
		tokenBucketScript,
		[]string{limiter.redisKey(request.Key)},
		limiter.now().UnixMilli(),
		limiter.ratePerSecond,
		limiter.burst,
		limiter.ttl.Milliseconds(),
	).Result()
	if err != nil {
		return gateway.RateLimitDecision{}, err
	}
	return parseDecision(result)
}

func (limiter *Limiter) redisKey(key string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(key)))
	return limiter.keyPrefix + ":" + string(limiter.scope) + ":" + hex.EncodeToString(digest[:])
}

func parseDecision(raw any) (gateway.RateLimitDecision, error) {
	values, ok := raw.([]interface{})
	if !ok || len(values) != 3 {
		return gateway.RateLimitDecision{}, fmt.Errorf("unexpected Redis token bucket result %#v", raw)
	}
	allowed, err := asInt64(values[0])
	if err != nil || (allowed != 0 && allowed != 1) {
		return gateway.RateLimitDecision{}, errors.New("invalid Redis token bucket allowed flag")
	}
	remainingFloat, err := strconv.ParseFloat(fmt.Sprint(values[1]), 64)
	if err != nil || remainingFloat < 0 || math.IsNaN(remainingFloat) || math.IsInf(remainingFloat, 0) {
		return gateway.RateLimitDecision{}, errors.New("invalid Redis token bucket remaining value")
	}
	retryMilliseconds, err := asInt64(values[2])
	if err != nil || retryMilliseconds < 0 || retryMilliseconds > math.MaxInt64/int64(time.Millisecond) {
		return gateway.RateLimitDecision{}, errors.New("invalid Redis token bucket retry value")
	}
	decision := gateway.RateLimitDecision{
		Allowed:    allowed == 1,
		Remaining:  int64(math.Floor(remainingFloat)),
		RetryAfter: time.Duration(retryMilliseconds) * time.Millisecond,
	}
	if !decision.Allowed {
		decision.Reason = "rate limit exceeded"
	}
	return decision, nil
}

func asInt64(value any) (int64, error) {
	switch typed := value.(type) {
	case int64:
		return typed, nil
	case int:
		return int64(typed), nil
	case string:
		return strconv.ParseInt(typed, 10, 64)
	case []byte:
		return strconv.ParseInt(string(typed), 10, 64)
	default:
		return 0, fmt.Errorf("unexpected integer value %#v", value)
	}
}

func validScope(scope gateway.RateLimitScope) bool {
	switch scope {
	case gateway.RateLimitScopeClientIP, gateway.RateLimitScopeUserID, gateway.RateLimitScopeUpstream:
		return true
	default:
		return false
	}
}

func isNilScriptRunner(runner ScriptRunner) bool {
	if runner == nil {
		return true
	}
	value := reflect.ValueOf(runner)
	return value.Kind() == reflect.Pointer && value.IsNil()
}
