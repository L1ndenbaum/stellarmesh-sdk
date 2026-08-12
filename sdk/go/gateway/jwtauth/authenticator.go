// Package jwtauth 提供约束明确的 HS256 Bearer token 认证组件。
package jwtauth

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"time"

	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/gateway"
	"github.com/golang-jwt/jwt/v5"
)

const minimumHMACSecretBytes = 32

// Claims 是默认支持的 JWT 身份字段。
type Claims struct {
	Roles []string `json:"roles"`
	jwt.RegisteredClaims
}

// ClaimsFactory 为每次认证创建独立 Claims，避免并发复用状态。
type ClaimsFactory func() jwt.Claims

// IdentityMapper 把已经完成标准验证的 Claims 转成网关身份。
type IdentityMapper func(jwt.Claims) (gateway.Identity, error)

// Config 配置固定算法、签发方、受众和身份映射。
type Config struct {
	Secret         []byte
	Issuer         string
	Audience       string
	Leeway         time.Duration
	Now            func() time.Time
	ClaimsFactory  ClaimsFactory
	IdentityMapper IdentityMapper
}

// Authenticator 校验 HS256 token 并实现 gateway.Authenticator。
type Authenticator struct {
	secret         []byte
	parser         *jwt.Parser
	claimsFactory  ClaimsFactory
	identityMapper IdentityMapper
}

// New 校验所有安全参数并构造认证组件。
func New(config Config) (*Authenticator, error) {
	if len(config.Secret) < minimumHMACSecretBytes {
		return nil, errors.New("JWT HS256 secret must contain at least 32 bytes")
	}
	issuer := strings.TrimSpace(config.Issuer)
	if issuer == "" {
		return nil, errors.New("JWT issuer is required")
	}
	audience := strings.TrimSpace(config.Audience)
	if audience == "" {
		return nil, errors.New("JWT audience is required")
	}
	if config.Leeway < 0 {
		return nil, errors.New("JWT leeway cannot be negative")
	}
	leeway := config.Leeway
	if leeway == 0 {
		leeway = 30 * time.Second
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	claimsFactory := config.ClaimsFactory
	if claimsFactory == nil {
		claimsFactory = func() jwt.Claims { return &Claims{} }
	}
	if isNilClaims(claimsFactory()) {
		return nil, errors.New("JWT claims factory returned nil")
	}
	identityMapper := config.IdentityMapper
	if identityMapper == nil {
		identityMapper = defaultIdentityMapper
	}
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(issuer),
		jwt.WithAudience(audience),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(leeway),
		jwt.WithTimeFunc(now),
	)
	return &Authenticator{
		secret:         append([]byte(nil), config.Secret...),
		parser:         parser,
		claimsFactory:  claimsFactory,
		identityMapper: identityMapper,
	}, nil
}

// Authenticate 把令牌格式或 Claims 错误作为正常拒绝，避免泄露验证细节。
func (authenticator *Authenticator) Authenticate(_ context.Context, rawToken string) (gateway.AuthenticationDecision, error) {
	if strings.TrimSpace(rawToken) == "" {
		return rejectedDecision(), nil
	}
	claims := authenticator.claimsFactory()
	if isNilClaims(claims) {
		return gateway.AuthenticationDecision{}, errors.New("JWT claims factory returned nil")
	}
	token, err := authenticator.parser.ParseWithClaims(rawToken, claims, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, errors.New("unexpected JWT signing method")
		}
		return authenticator.secret, nil
	})
	if err != nil || token == nil || !token.Valid {
		return rejectedDecision(), nil
	}
	subject, err := claims.GetSubject()
	if err != nil || strings.TrimSpace(subject) == "" {
		return rejectedDecision(), nil
	}
	identity, err := authenticator.identityMapper(claims)
	if err != nil || strings.TrimSpace(identity.UserID) == "" {
		return rejectedDecision(), nil
	}
	identity.UserID = strings.TrimSpace(identity.UserID)
	identity.Roles = normalizeRoles(identity.Roles)
	return gateway.AuthenticationDecision{Authenticated: true, Identity: identity}, nil
}

func defaultIdentityMapper(raw jwt.Claims) (gateway.Identity, error) {
	claims, ok := raw.(*Claims)
	if !ok {
		return gateway.Identity{}, errors.New("default JWT mapper requires jwtauth.Claims")
	}
	return gateway.Identity{UserID: claims.Subject, Roles: claims.Roles}, nil
}

func rejectedDecision() gateway.AuthenticationDecision {
	return gateway.AuthenticationDecision{Authenticated: false, Reason: "invalid bearer token"}
}

func normalizeRoles(roles []string) []string {
	result := make([]string, 0, len(roles))
	seen := make(map[string]struct{}, len(roles))
	for _, role := range roles {
		role = strings.TrimSpace(role)
		if role == "" {
			continue
		}
		if _, exists := seen[role]; exists {
			continue
		}
		seen[role] = struct{}{}
		result = append(result, role)
	}
	return result
}

func isNilClaims(claims jwt.Claims) bool {
	if claims == nil {
		return true
	}
	value := reflect.ValueOf(claims)
	return value.Kind() == reflect.Pointer && value.IsNil()
}
