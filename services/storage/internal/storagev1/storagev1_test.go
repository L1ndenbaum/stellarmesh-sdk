package storagev1_test

import (
	"strings"
	"testing"
	"time"

	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/objectstorage"
	"github.com/L1ndenbaum/stellarmesh-sdk/services/storage/internal/storagev1"
)

func TestCompilePolicyRejectsEmptyAndDuplicateCapabilities(t *testing.T) {
	t.Parallel()
	validToken := "12345678901234567890123456789012"
	base := storagev1.AccessConfig{
		Namespaces: map[string]storagev1.NamespaceConfig{"documents": {Bucket: "bucket", Prefix: ""}},
		Principals: map[string]storagev1.PrincipalConfig{"backend": {Tokens: []string{validToken}, Grants: map[string][]storagev1.Capability{"documents": {storagev1.CapabilityRead}}}},
	}
	if _, err := storagev1.CompilePolicy(base); err != nil {
		t.Fatalf("CompilePolicy() error = %v", err)
	}
	base.Principals["backend"] = storagev1.PrincipalConfig{Tokens: []string{validToken}, Grants: map[string][]storagev1.Capability{"documents": {storagev1.CapabilityRead, storagev1.CapabilityRead}}}
	if _, err := storagev1.CompilePolicy(base); err == nil {
		t.Fatal("CompilePolicy() 应拒绝重复 capability")
	}
	if _, err := storagev1.DecodePolicy(strings.NewReader(`{"namespaces":{},"principals":{}} trailing`)); err == nil {
		t.Fatal("DecodePolicy() 应拒绝尾部内容")
	}
}

func TestValidateObjectInputAndTTL(t *testing.T) {
	t.Parallel()
	namespace := objectstorage.Namespace{Bucket: "bucket", Prefix: "prefix/"}
	if err := storagev1.ValidateObjectInput(namespace, "key", "version", "text/plain", map[string]string{"source": "test"}); err != nil {
		t.Fatalf("ValidateObjectInput() error = %v", err)
	}
	if err := storagev1.ValidateObjectInput(namespace, "key", "", "", map[string]string{"bad\n": "value"}); err == nil {
		t.Fatal("ValidateObjectInput() 应拒绝控制字符")
	}
	if ttl, err := storagev1.PresignTTL(0); err != nil || ttl != 15*time.Minute {
		t.Fatalf("PresignTTL() = %s, %v", ttl, err)
	}
	if _, err := storagev1.PresignTTL(59); err == nil {
		t.Fatal("PresignTTL() 应拒绝过短 TTL")
	}
	if err := storagev1.ValidateNamespaceName("Bad"); err == nil {
		t.Fatal("ValidateNamespaceName() 应拒绝大写")
	}
}

func TestStrictDecodeRejectsUnknownTopLevelField(t *testing.T) {
	t.Parallel()
	payload := `{"namespaces":{"documents":{"bucket":"bucket","prefix":""}},"principals":{"backend":{"tokens":["12345678901234567890123456789012"],"grants":{"documents":["read"]}}},"unknown":true}`
	if _, err := storagev1.DecodePolicy(strings.NewReader(payload)); err == nil {
		t.Fatal("DecodePolicy() 应拒绝未知字段")
	}
}
