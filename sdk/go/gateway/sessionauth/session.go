package sessionauth

import (
	"errors"
	"time"

	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/gateway"
)

// ErrCorruptSession 表示存储结构或身份损坏，必须按组件故障处理。
var ErrCorruptSession = errors.New("corrupt session storage")

// ErrSessionCollision 表示多次随机标识冲突；已有会话不会被覆盖。
var ErrSessionCollision = errors.New("session ID collision")

// Session 是 Redis 中有效会话的快照；ID 是秘密凭证，不应写入日志。
// Attributes 经 JSON 往返，数字解码为 float64，不保留业务自定义 Go 类型。
type Session struct {
	ID        string
	Identity  gateway.Identity
	CreatedAt time.Time
	ExpiresAt time.Time
}

// CreateOptions 创建带明确有效期的会话，不接受调用方指定的 Session ID。
type CreateOptions struct {
	Identity gateway.Identity
	// TTL 至少 1ms，不提供永久会话；过期时刻以 Redis 服务端时间计算。
	TTL time.Duration
}
