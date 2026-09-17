package gateway

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

const (
	headerXUserID    = "X-User-ID"
	headerXUserRoles = "X-User-Roles"
)

// Identity 是网关确认后可以安全传给上游的调用方身份。
type Identity struct {
	UserID     string
	Roles      []string
	Attributes map[string]any
}

// AuthenticationDecision 区分凭据拒绝和认证组件自身故障。
type AuthenticationDecision struct {
	Authenticated bool
	Identity      Identity
	Reason        string
}

// Authenticator 校验 Bearer token；返回错误表示组件故障并触发 fail-close。
type Authenticator interface {
	Authenticate(context.Context, string) (AuthenticationDecision, error)
}

// AuthenticatorFunc 让函数直接实现 Authenticator。
type AuthenticatorFunc func(context.Context, string) (AuthenticationDecision, error)

// Authenticate 调用认证函数。
func (authenticate AuthenticatorFunc) Authenticate(ctx context.Context, token string) (AuthenticationDecision, error) {
	return authenticate(ctx, token)
}

// WithAuthenticator 启用受保护路由认证。
func WithAuthenticator(authenticator Authenticator) Option {
	return componentOption("authenticator", func(config *config) error {
		if isNilInterface(authenticator) {
			return errors.New("gateway authenticator is nil")
		}
		config.authenticator = authenticator
		return nil
	})
}

func (gateway *Gateway) authenticate(w http.ResponseWriter, r *http.Request, route Route) (*Identity, bool) {
	if route.Access == AccessPublic {
		setAuthResult(r, "public", nil)
		return nil, true
	}
	if gateway.authenticator == nil {
		setAuthResult(r, "error", nil)
		gateway.fail(w, r, unavailableError("authenticator_unavailable", errors.New("gateway authenticator is not configured")))
		return nil, false
	}
	raw := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(raw) <= len("Bearer ") || !strings.EqualFold(raw[:len("Bearer ")], "Bearer ") {
		setAuthResult(r, "missing_token", nil)
		gateway.fail(w, r, GatewayError{Status: http.StatusUnauthorized, Code: "missing_bearer_token", Message: "unauthorized"})
		return nil, false
	}
	token := strings.TrimSpace(raw[len("Bearer "):])
	if token == "" {
		setAuthResult(r, "missing_token", nil)
		gateway.fail(w, r, GatewayError{Status: http.StatusUnauthorized, Code: "missing_bearer_token", Message: "unauthorized"})
		return nil, false
	}
	decision, err := gateway.authenticator.Authenticate(r.Context(), token)
	if err != nil {
		setAuthResult(r, "error", nil)
		gateway.fail(w, r, unavailableError("authentication_failed", err))
		return nil, false
	}
	if !decision.Authenticated || strings.TrimSpace(decision.Identity.UserID) == "" {
		setAuthResult(r, "invalid_token", nil)
		gateway.fail(w, r, GatewayError{Status: http.StatusUnauthorized, Code: "invalid_bearer_token", Message: "unauthorized"})
		return nil, false
	}
	identity := cloneIdentity(decision.Identity)
	setAuthResult(r, "success", &identity)
	return &identity, true
}

func stripAndInjectIdentity(header http.Header, identity *Identity) {
	header.Del(headerXUserID)
	header.Del(headerXUserRoles)
	if identity == nil {
		return
	}
	header.Set(headerXUserID, identity.UserID)
	if len(identity.Roles) > 0 {
		header.Set(headerXUserRoles, strings.Join(identity.Roles, ","))
	}
}

func cloneIdentity(identity Identity) Identity {
	identity.Roles = append([]string(nil), identity.Roles...)
	if identity.Attributes != nil {
		attributes := make(map[string]any, len(identity.Attributes))
		for key, value := range identity.Attributes {
			attributes[key] = value
		}
		identity.Attributes = attributes
	}
	return identity
}

// IdentityFromContext 返回 SDK 认证后的身份副本。
func IdentityFromContext(ctx context.Context) (Identity, bool) {
	identity, ok := ctx.Value(identityContextKey).(Identity)
	return cloneIdentity(identity), ok
}
