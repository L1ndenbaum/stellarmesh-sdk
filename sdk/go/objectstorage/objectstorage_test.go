package objectstorage_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/objectstorage"
)

func TestNormalizeNamespace(t *testing.T) {
	t.Parallel()
	namespace, err := objectstorage.NormalizeNamespace(objectstorage.Namespace{Bucket: "documents", Prefix: "tenant/a///"})
	if err != nil {
		t.Fatalf("NormalizeNamespace() error = %v", err)
	}
	if namespace.Prefix != "tenant/a/" {
		t.Fatalf("Prefix = %q", namespace.Prefix)
	}
	for _, invalid := range []objectstorage.Namespace{
		{},
		{Bucket: " documents"},
		{Bucket: "documents", Prefix: "/root"},
		{Bucket: "documents", Prefix: "bad\n"},
	} {
		_, err := objectstorage.NormalizeNamespace(invalid)
		if !errors.Is(err, objectstorage.ErrInvalidArgument) {
			t.Fatalf("NormalizeNamespace(%+v) error = %v", invalid, err)
		}
	}
}

func TestValidateKeyPreservesMiddleContent(t *testing.T) {
	t.Parallel()
	namespace := objectstorage.Namespace{Bucket: "documents", Prefix: "prefix/"}
	if err := objectstorage.ValidateKey(namespace, "a//../b"); err != nil {
		t.Fatalf("ValidateKey() error = %v", err)
	}
	for _, key := range []string{"", "/root", " trailing ", "bad\x00key", strings.Repeat("界", 400)} {
		if err := objectstorage.ValidateKey(namespace, key); !errors.Is(err, objectstorage.ErrInvalidArgument) {
			t.Fatalf("ValidateKey(%q) error = %v", key, err)
		}
	}
}

func TestValidateTTL(t *testing.T) {
	t.Parallel()
	ttl, err := objectstorage.ValidateTTL(0, 15*time.Minute)
	if err != nil || ttl != 15*time.Minute {
		t.Fatalf("ValidateTTL() = %v, %v", ttl, err)
	}
	for _, invalid := range []time.Duration{time.Second, 61 * time.Minute} {
		if _, err := objectstorage.ValidateTTL(invalid, 15*time.Minute); !errors.Is(err, objectstorage.ErrInvalidArgument) {
			t.Fatalf("ValidateTTL(%s) error = %v", invalid, err)
		}
	}
}

func TestErrorSupportsErrorsIsAndHidesProviderDetails(t *testing.T) {
	t.Parallel()
	err := &objectstorage.Error{
		Kind:      objectstorage.ErrUnavailable,
		Operation: "get",
		Key:       "logical-key",
		Err:       errors.New("https://secret.example/?X-Amz-Signature=secret"),
	}
	if !errors.Is(err, objectstorage.ErrUnavailable) {
		t.Fatal("Error 不支持 errors.Is")
	}
	if strings.Contains(err.Error(), "secret.example") || strings.Contains(err.Error(), "Signature") {
		t.Fatalf("Error() 泄露底层信息: %s", err)
	}
}
