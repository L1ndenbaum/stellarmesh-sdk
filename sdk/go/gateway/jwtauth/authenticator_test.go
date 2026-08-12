package jwtauth

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/gateway"
	"github.com/golang-jwt/jwt/v5"
)

var testSecret = []byte("0123456789abcdef0123456789abcdef")
var testNow = time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)

func TestAuthenticatorAcceptsValidHS256Token(t *testing.T) {
	authenticator := mustAuthenticator(t, Config{Secret: testSecret, Issuer: "issuer", Audience: "audience", Now: func() time.Time { return testNow }})
	token := signToken(t, jwt.SigningMethodHS256, &Claims{
		Roles: []string{" admin ", "admin", "reader"},
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer: "issuer", Subject: "user-1", Audience: jwt.ClaimStrings{"audience"},
			ExpiresAt: jwt.NewNumericDate(testNow.Add(time.Minute)),
		},
	})
	decision, err := authenticator.Authenticate(context.Background(), token)
	if err != nil || !decision.Authenticated || decision.Identity.UserID != "user-1" {
		t.Fatalf("Authenticate() = %#v, %v", decision, err)
	}
	if strings.Join(decision.Identity.Roles, ",") != "admin,reader" {
		t.Fatalf("roles = %#v", decision.Identity.Roles)
	}
}

func TestAuthenticatorRejectsWrongAlgorithmAndMissingExpiration(t *testing.T) {
	authenticator := mustAuthenticator(t, Config{Secret: testSecret, Issuer: "issuer", Audience: "audience", Now: func() time.Time { return testNow }})
	tests := []string{
		signToken(t, jwt.SigningMethodHS384, &Claims{RegisteredClaims: validRegisteredClaims()}),
		signToken(t, jwt.SigningMethodHS256, &Claims{RegisteredClaims: jwt.RegisteredClaims{
			Issuer: "issuer", Subject: "user-1", Audience: jwt.ClaimStrings{"audience"},
		}}),
	}
	for _, token := range tests {
		decision, err := authenticator.Authenticate(context.Background(), token)
		if err != nil || decision.Authenticated {
			t.Fatalf("Authenticate() = %#v, %v", decision, err)
		}
	}
}

func TestAuthenticatorAppliesDefaultLeeway(t *testing.T) {
	authenticator := mustAuthenticator(t, Config{Secret: testSecret, Issuer: "issuer", Audience: "audience", Now: func() time.Time { return testNow }})
	claims := validRegisteredClaims()
	claims.ExpiresAt = jwt.NewNumericDate(testNow.Add(-10 * time.Second))
	decision, err := authenticator.Authenticate(context.Background(), signToken(t, jwt.SigningMethodHS256, &Claims{RegisteredClaims: claims}))
	if err != nil || !decision.Authenticated {
		t.Fatalf("Authenticate() = %#v, %v", decision, err)
	}
}

func TestAuthenticatorSupportsCustomClaimsMapping(t *testing.T) {
	type tenantClaims struct {
		Tenant string `json:"tenant"`
		jwt.RegisteredClaims
	}
	authenticator := mustAuthenticator(t, Config{
		Secret: testSecret, Issuer: "issuer", Audience: "audience", Now: func() time.Time { return testNow },
		ClaimsFactory: func() jwt.Claims { return &tenantClaims{} },
		IdentityMapper: func(raw jwt.Claims) (gateway.Identity, error) {
			claims := raw.(*tenantClaims)
			return gateway.Identity{UserID: claims.Subject, Attributes: map[string]any{"tenant": claims.Tenant}}, nil
		},
	})
	claims := &tenantClaims{Tenant: "tenant-1", RegisteredClaims: validRegisteredClaims()}
	decision, err := authenticator.Authenticate(context.Background(), signToken(t, jwt.SigningMethodHS256, claims))
	if err != nil || !decision.Authenticated || decision.Identity.Attributes["tenant"] != "tenant-1" {
		t.Fatalf("Authenticate() = %#v, %v", decision, err)
	}
}

func TestNewRejectsUnsafeConfiguration(t *testing.T) {
	tests := []Config{
		{Secret: []byte("short"), Issuer: "issuer", Audience: "audience"},
		{Secret: testSecret, Audience: "audience"},
		{Secret: testSecret, Issuer: "issuer"},
		{Secret: testSecret, Issuer: "issuer", Audience: "audience", Leeway: -time.Second},
	}
	for _, config := range tests {
		if _, err := New(config); err == nil {
			t.Fatalf("New() accepted %#v", config)
		}
	}
}

func mustAuthenticator(t *testing.T, config Config) *Authenticator {
	t.Helper()
	authenticator, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	return authenticator
}

func validRegisteredClaims() jwt.RegisteredClaims {
	return jwt.RegisteredClaims{
		Issuer: "issuer", Subject: "user-1", Audience: jwt.ClaimStrings{"audience"},
		ExpiresAt: jwt.NewNumericDate(testNow.Add(time.Minute)),
	}
}

func signToken(t *testing.T, method jwt.SigningMethod, claims jwt.Claims) string {
	t.Helper()
	value, err := jwt.NewWithClaims(method, claims).SignedString(testSecret)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
