package sessionauth

import (
	"context"
	"errors"
	"strings"

	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/gateway"
)

// SessionReader 是认证器需要的最小读取能力；found=true 必须表示仍有效的会话。
type SessionReader interface {
	Lookup(context.Context, string) (Session, bool, error)
}

// Authenticator 将有效会话转换为网关身份，不续期、不写 Cookie。
type Authenticator struct{ reader SessionReader }

// NewAuthenticator 拒绝空读取器；存储故障不会被转换为普通认证拒绝。
func NewAuthenticator(reader SessionReader) (*Authenticator, error) {
	if nilInterface(reader) {
		return nil, errors.New("session reader is required")
	}
	return &Authenticator{reader: reader}, nil
}

// Authenticate 验证 Session ID 并读取当前会话。
func (auth *Authenticator) Authenticate(ctx context.Context, credential string) (gateway.AuthenticationDecision, error) {
	if !validSessionID(credential) {
		return gateway.AuthenticationDecision{}, nil
	}
	session, found, err := auth.reader.Lookup(ctx, credential)
	if err != nil {
		return gateway.AuthenticationDecision{}, err
	}
	if !found {
		return gateway.AuthenticationDecision{}, nil
	}
	if session.ID != credential || strings.TrimSpace(session.Identity.UserID) == "" || session.CreatedAt.IsZero() || !session.ExpiresAt.After(session.CreatedAt) {
		return gateway.AuthenticationDecision{}, ErrCorruptSession
	}
	return gateway.AuthenticationDecision{Authenticated: true, Identity: session.Identity}, nil
}
