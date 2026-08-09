package auth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuthenticatorBindsAndRotatesServiceTokens(t *testing.T) {
	authenticator, err := New(FileConfig{Services: map[string][]string{
		"orders": {strings.Repeat("a", minimumTokenLength), strings.Repeat("b", minimumTokenLength)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{strings.Repeat("a", minimumTokenLength), strings.Repeat("b", minimumTokenLength)} {
		service, ok := authenticator.Authenticate(token)
		if !ok || service != "orders" {
			t.Fatalf("Authenticate() = %q, %t", service, ok)
		}
	}
	if _, ok := authenticator.Authenticate(strings.Repeat("c", minimumTokenLength)); ok {
		t.Fatal("Authenticate() accepted an unknown token")
	}
}

func TestAuthenticatorRejectsWeakAndDuplicateTokens(t *testing.T) {
	if _, err := New(FileConfig{Services: map[string][]string{"orders": {"short"}}}); err == nil {
		t.Fatal("New() accepted a short token")
	}
	token := strings.Repeat("a", minimumTokenLength)
	if _, err := New(FileConfig{Services: map[string][]string{"orders": {token}, "billing": {token}}}); err == nil {
		t.Fatal("New() accepted a token shared by services")
	}
}

func TestLoadFileRejectsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(path, []byte(`{"services":{"orders":["aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"]},"unknown":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFile(path); err == nil {
		t.Fatal("LoadFile() accepted an unknown field")
	}
}
