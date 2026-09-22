package sessionauth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/gateway"
	"github.com/redis/go-redis/v9"
)

// RedisClient 是单实例或 Sentinel Client 与测试替身所需的最小接口。
// Eval 内部访问同一主节点的多个键，不支持 ClusterClient 或 Cluster 包装器。
type RedisClient interface {
	Eval(context.Context, string, []string, ...interface{}) *redis.Cmd
}

// StoreConfig 由业务方提供连接和项目隔离配置；Client 不由 Store 关闭。
type StoreConfig struct {
	// Client 由业务创建与关闭；要求单实例或 Sentinel，不支持 Cluster。
	Client RedisClient
	// ProjectScope 为项目隔离标识，不能为空，不自动裁剪；由 KeyBuilder 转义。
	ProjectScope string
	// KeySeparator 留空使用冒号；只允许不含百分号和大括号的 ASCII 标点。
	KeySeparator string
	// MaxSessionsPerUser 为 0 时使用 10；有效范围为 1 至 2^53-1。满额创建撤销最早会话。
	MaxSessionsPerUser int
}

// Store 原子维护会话及用户索引；同一项目的实例必须使用一致配置。
type Store struct {
	client RedisClient
	scope  string
	keys   KeyBuilder
	limit  int
}

// NewStore 校验本地配置，不建立连接或探测 Redis。
func NewStore(config StoreConfig) (*Store, error) {
	if nilInterface(config.Client) {
		return nil, errors.New("session Redis client is required")
	}
	if _, ok := config.Client.(*redis.ClusterClient); ok {
		return nil, errors.New("session store does not support Redis Cluster")
	}
	if config.ProjectScope == "" {
		return nil, errors.New("session project scope is required")
	}
	if config.MaxSessionsPerUser < 0 || uint64(config.MaxSessionsPerUser) > 1<<53-1 {
		return nil, errors.New("invalid maximum sessions per user")
	}
	limit := config.MaxSessionsPerUser
	if limit == 0 {
		limit = 10
	}
	keys, err := NewKeyBuilder(KeyConfig{Separator: config.KeySeparator})
	if err != nil {
		return nil, err
	}
	return &Store{client: config.Client, scope: config.ProjectScope, keys: *keys, limit: limit}, nil
}

// Create 生成随机凭证；满额时原子撤销最早创建的会话，不覆盖冲突 ID。
func (store *Store) Create(ctx context.Context, options CreateOptions) (Session, error) {
	if strings.TrimSpace(options.Identity.UserID) == "" || !utf8.ValidString(options.Identity.UserID) {
		return Session{}, errors.New("session user ID is required")
	}
	if err := validateTTL(options.TTL); err != nil {
		return Session{}, err
	}
	identity, err := json.Marshal(options.Identity)
	if err != nil {
		return Session{}, fmt.Errorf("encode session identity: %w", err)
	}
	for attempt := 0; attempt < 3; attempt++ {
		var random [32]byte
		if _, err := rand.Read(random[:]); err != nil {
			return Session{}, err
		}
		id := base64.RawURLEncoding.EncodeToString(random[:])
		values, err := store.run(ctx, "create", id, options.Identity.UserID, options.TTL, string(identity))
		if errors.Is(err, ErrSessionCollision) {
			continue
		}
		if err != nil {
			return Session{}, err
		}
		if len(values) != 1 {
			return Session{}, ErrCorruptSession
		}
		session, err := decodeSession(values[0])
		if err == nil && (session.ID != id || session.Identity.UserID != options.Identity.UserID) {
			err = ErrCorruptSession
		}
		return session, err
	}
	return Session{}, ErrSessionCollision
}

// Lookup 返回当前有效会话，缺失或过期时 found=false。
func (store *Store) Lookup(ctx context.Context, id string) (Session, bool, error) {
	if !validSessionID(id) {
		return Session{}, false, nil
	}
	return store.read(ctx, "lookup", id, 0)
}

// Renew 从 Redis 当前时间续期，不改变创建顺序，不复活已失效会话。
func (store *Store) Renew(ctx context.Context, id string, ttl time.Duration) (Session, bool, error) {
	if err := validateTTL(ttl); err != nil {
		return Session{}, false, err
	}
	if !validSessionID(id) {
		return Session{}, false, nil
	}
	return store.read(ctx, "renew", id, ttl)
}
func (store *Store) read(ctx context.Context, op, id string, ttl time.Duration) (Session, bool, error) {
	values, err := store.run(ctx, op, id, "", ttl, "")
	if err != nil {
		return Session{}, false, err
	}
	if len(values) == 0 {
		return Session{}, false, nil
	}
	session, err := decodeSession(values[0])
	if err == nil && session.ID != id {
		err = ErrCorruptSession
	}
	return session, err == nil, err
}

// Revoke 幂等撤销单个会话，不影响该用户的其他会话。
func (store *Store) Revoke(ctx context.Context, id string) error {
	if !validSessionID(id) {
		return nil
	}
	_, err := store.run(ctx, "revoke", id, "", 0, "")
	return err
}

// ListByUser 按创建时间返回用户当前有效会话；ID 是凭证，禁止直接暴露给无权调用方。
func (store *Store) ListByUser(ctx context.Context, userID string) ([]Session, error) {
	if strings.TrimSpace(userID) == "" {
		return nil, errors.New("session user ID is required")
	}
	values, err := store.run(ctx, "list", "", userID, 0, "")
	if err != nil {
		return nil, err
	}
	sessions := make([]Session, 0, len(values))
	for _, value := range values {
		s, err := decodeSession(value)
		if err != nil {
			return nil, err
		}
		if s.Identity.UserID != userID {
			return nil, ErrCorruptSession
		}
		sessions = append(sessions, s)
	}
	return sessions, nil
}

// RevokeByUser 原子撤销用户当前全部会话；操作之后的新登录仍允许创建。
func (store *Store) RevokeByUser(ctx context.Context, userID string) error {
	if strings.TrimSpace(userID) == "" {
		return errors.New("session user ID is required")
	}
	_, err := store.run(ctx, "revoke_user", "", userID, 0, "")
	return err
}

func (store *Store) run(ctx context.Context, op, id, user string, ttl time.Duration, identity string) ([]interface{}, error) {
	key, _ := store.keys.SessionKey(store.scope, id)
	if op == "list" || op == "revoke_user" {
		key, _ = store.keys.UserSessionsKey(store.scope, user)
	}
	raw, err := store.client.Eval(ctx, sessionScript, []string{key}, op, id, user, ttl.Milliseconds(), identity, store.limit, store.keys.prefix(store.scope, "session"), store.keys.prefix(store.scope, "user_sessions"), store.keys.delimiter()).Result()
	if err != nil {
		if strings.Contains(err.Error(), "SESSION_CORRUPT") {
			return nil, fmt.Errorf("%w: %w", ErrCorruptSession, err)
		}
		return nil, fmt.Errorf("session Redis operation failed: %w", err)
	}
	result, ok := raw.([]interface{})
	if !ok || len(result) == 0 {
		return nil, ErrCorruptSession
	}
	switch result[0] {
	case "ok":
		if (op == "create" || op == "lookup" || op == "renew") && len(result) != 2 {
			return nil, ErrCorruptSession
		}
		if (op == "revoke" || op == "revoke_user") && len(result) != 1 {
			return nil, ErrCorruptSession
		}
		return result[1:], nil
	case "missing":
		if len(result) != 1 || (op != "lookup" && op != "renew" && op != "revoke") {
			return nil, ErrCorruptSession
		}
		return nil, nil
	case "collision":
		if op != "create" || len(result) != 1 {
			return nil, ErrCorruptSession
		}
		return nil, ErrSessionCollision
	default:
		return nil, ErrCorruptSession
	}
}

type storedSession struct {
	Version   int    `json:"version"`
	ID        string `json:"id"`
	UserID    string `json:"user_id"`
	Identity  string `json:"identity"`
	CreatedAt int64  `json:"created_at"`
	ExpiresAt int64  `json:"expires_at"`
}

func decodeSession(raw interface{}) (Session, error) {
	value, ok := raw.(string)
	if !ok {
		return Session{}, ErrCorruptSession
	}
	var record storedSession
	if err := json.Unmarshal([]byte(value), &record); err != nil {
		return Session{}, ErrCorruptSession
	}
	var identity gateway.Identity
	if err := json.Unmarshal([]byte(record.Identity), &identity); err != nil {
		return Session{}, ErrCorruptSession
	}
	if record.Version != 1 || !validSessionID(record.ID) || strings.TrimSpace(identity.UserID) == "" || identity.UserID != record.UserID || record.CreatedAt <= 0 || record.ExpiresAt <= record.CreatedAt {
		return Session{}, ErrCorruptSession
	}
	return Session{ID: record.ID, Identity: identity, CreatedAt: time.UnixMilli(record.CreatedAt).UTC(), ExpiresAt: time.UnixMilli(record.ExpiresAt).UTC()}, nil
}
func validSessionID(id string) bool {
	if len(id) != 43 {
		return false
	}
	bytes, err := base64.RawURLEncoding.DecodeString(id)
	return err == nil && len(bytes) == 32 && base64.RawURLEncoding.EncodeToString(bytes) == id
}
func validateTTL(ttl time.Duration) error {
	if ttl < time.Millisecond {
		return errors.New("session TTL must be at least one millisecond")
	}
	return nil
}
func nilInterface(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	}
	return false
}

// sessionScript 只在单个 Redis 主节点运行；派生键始终限制在配置的项目命名空间。
// 所有可预见的数据校验都在写操作之前完成：Lua 的运行时错误不会回滚已执行命令。
// identity 保留原始 JSON 字符串，避免 cjson 重编码空数组或改变大整数。
// identity_hash 只检测意外的数据损坏，不是签名；Redis 写权限始终属于可信边界。
const sessionScript = `
local op, id, user = ARGV[1], ARGV[2], ARGV[3]
local ttl, identity, limit = tonumber(ARGV[4]), ARGV[5], tonumber(ARGV[6])
local session_prefix, user_prefix, separator = ARGV[7], ARGV[8], ARGV[9]
local clock = redis.call('TIME')
local now = tonumber(clock[1]) * 1000 + math.floor(tonumber(clock[2]) / 1000)
local function escape(value)
 return (string.gsub(value, '.', function(c)
  local b = string.byte(c)
  if b <= 32 or b >= 127 or c == '%' or c == '{' or c == '}' or string.find(separator, c, 1, true) then
   return string.format('%%%02X', b)
  end
  return c
 end))
end
local function kind(key) return redis.call('TYPE', key).ok end
local function load(sid)
 local key = session_prefix .. escape(sid)
 local k = kind(key)
 if k == 'none' then return nil end
 if k ~= 'string' then error('SESSION_CORRUPT') end
 local raw = redis.call('GET', key)
 local success, row = pcall(cjson.decode, raw)
 if not success or type(row) ~= 'table' or row.version ~= 1 or row.id ~= sid or
  type(row.identity) ~= 'string' or row.identity_hash ~= redis.sha1hex(row.identity) or type(row.user_id) ~= 'string' or row.user_id == '' or type(row.identity) ~= 'string' or
  type(row.created_at) ~= 'number' or type(row.expires_at) ~= 'number' or
  row.created_at <= 0 or row.created_at > now or row.expires_at <= row.created_at or
  row.created_at % 1 ~= 0 or row.expires_at % 1 ~= 0 or row.expires_at > 9007199254740991 or
  #sid ~= 43 or string.find(sid, '[^%w_-]') then error('SESSION_CORRUPT') end
 local valid, ident = pcall(cjson.decode, row.identity)
 if not valid or type(ident) ~= 'table' or ident.UserID ~= row.user_id then error('SESSION_CORRUPT') end
 if ident.Roles ~= nil and ident.Roles ~= cjson.null then
  if type(ident.Roles) ~= 'table' then error('SESSION_CORRUPT') end
  for k,v in pairs(ident.Roles) do
   if type(k) ~= 'number' or type(v) ~= 'string' then error('SESSION_CORRUPT') end
  end
 end
 if ident.Attributes ~= nil and ident.Attributes ~= cjson.null then
  if type(ident.Attributes) ~= 'table' then error('SESSION_CORRUPT') end
  for k,v in pairs(ident.Attributes) do if type(k) ~= 'string' then error('SESSION_CORRUPT') end end
 end
 local remaining = redis.call('PTTL', key)
 if remaining < 0 then error('SESSION_CORRUPT') end
 if row.expires_at <= now then return nil end
 return {row=row, raw=raw, key=key}
end
local target
if op == 'create' then
 if redis.call('EXISTS', KEYS[1]) == 1 then return {'collision'} end
elseif op == 'lookup' or op == 'renew' or op == 'revoke' then
 target = load(id)
 if not target then return {'missing'} end
 user = target.row.user_id
end
local index = user_prefix .. escape(user)
local index_kind = kind(index)
if index_kind ~= 'none' and index_kind ~= 'zset' then error('SESSION_CORRUPT') end
local members = redis.call('ZRANGE', index, 0, -1, 'WITHSCORES')
local active, stale = {}, {}
local target_found = false
for i=1,#members,2 do
 local sid, score = members[i], tonumber(members[i+1])
 local item = load(sid)
 if item then
  if item.row.user_id ~= user or item.row.created_at ~= score then error('SESSION_CORRUPT') end
  table.insert(active,item)
  if sid == id then target_found = true end
 else table.insert(stale,sid) end
end
if target and not target_found then error('SESSION_CORRUPT') end
local encoded
if op == 'create' then
 encoded = cjson.encode({version=1,id=id,user_id=user,identity=identity,identity_hash=redis.sha1hex(identity),created_at=now,expires_at=now+ttl})
elseif op == 'renew' then
 target.row.expires_at = now + ttl
 encoded = cjson.encode(target.row)
end
-- 预检完成后才开始改变任何记录或索引。
for _,sid in ipairs(stale) do
 redis.call('DEL',session_prefix .. escape(sid))
 redis.call('ZREM',index,sid)
end
if op == 'revoke_user' then
 for _,item in ipairs(active) do redis.call('DEL',item.key) end
 redis.call('DEL',index)
 return {'ok'}
end
if op == 'create' then
 while #active >= limit do
  local oldest = table.remove(active,1)
  redis.call('DEL',oldest.key)
  redis.call('ZREM',index,oldest.row.id)
 end
 redis.call('SET',KEYS[1],encoded,'PX',ttl)
 redis.call('ZADD',index,now,id)
 table.insert(active,{row={id=id,expires_at=now+ttl},raw=encoded,key=KEYS[1]})
elseif op == 'renew' then
 redis.call('SET',KEYS[1],encoded,'PX',ttl)
 for _,item in ipairs(active) do
  if item.row.id == id then item.row.expires_at=now+ttl; item.raw=encoded end
 end
elseif op == 'revoke' then
 redis.call('DEL',KEYS[1])
 redis.call('ZREM',index,id)
 for i,item in ipairs(active) do if item.row.id == id then table.remove(active,i);break end end
end
local expires = 0
for _,item in ipairs(active) do expires = math.max(expires,item.row.expires_at) end
if expires > now then redis.call('PEXPIREAT',index,expires) else redis.call('DEL',index) end
if op == 'create' or op == 'renew' then return {'ok',encoded} end
if op == 'lookup' then return {'ok',target.raw} end
local result = {'ok'}
if op == 'list' then for _,item in ipairs(active) do table.insert(result,item.raw) end end
return result
`
