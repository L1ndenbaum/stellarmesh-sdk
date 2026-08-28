package storagecontract_test

import (
	"strings"
	"testing"
	"time"

	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/objectstorage"
	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/storagecontract"
)

func TestCompilePolicyRejectsEmptyAndDuplicateCapabilities(t *testing.T) {
	t.Parallel()
	validToken := "12345678901234567890123456789012"
	base := storagecontract.AccessConfig{
		Namespaces: map[string]storagecontract.NamespaceConfig{"documents": {Bucket: "bucket", Prefix: ""}},
		Principals: map[string]storagecontract.PrincipalConfig{"backend": {Tokens: []string{validToken}, Grants: map[string][]storagecontract.Capability{"documents": {storagecontract.CapabilityRead}}}},
	}
	if _, err := storagecontract.CompilePolicy(base); err != nil {
		t.Fatalf("CompilePolicy() error = %v", err)
	}
	base.Principals["backend"] = storagecontract.PrincipalConfig{Tokens: []string{validToken}, Grants: map[string][]storagecontract.Capability{"documents": {storagecontract.CapabilityRead, storagecontract.CapabilityRead}}}
	if _, err := storagecontract.CompilePolicy(base); err == nil {
		t.Fatal("CompilePolicy() 应拒绝重复 capability")
	}
	if _, err := storagecontract.DecodePolicy(strings.NewReader(`{"namespaces":{},"principals":{}} trailing`)); err == nil {
		t.Fatal("DecodePolicy() 应拒绝尾部内容")
	}
}

func TestValidateObjectInputAndTTL(t *testing.T) {
	t.Parallel()
	namespace := objectstorage.Namespace{Bucket: "bucket", Prefix: "prefix/"}
	if err := storagecontract.ValidateObjectInput(namespace, "key", "version", "text/plain", map[string]string{"source": "test"}); err != nil {
		t.Fatalf("ValidateObjectInput() error = %v", err)
	}
	if err := storagecontract.ValidateObjectInput(namespace, "key", "", "", map[string]string{"bad\n": "value"}); err == nil {
		t.Fatal("ValidateObjectInput() 应拒绝控制字符")
	}
	if ttl, err := storagecontract.PresignTTL(0); err != nil || ttl != 15*time.Minute {
		t.Fatalf("PresignTTL() = %s, %v", ttl, err)
	}
	if _, err := storagecontract.PresignTTL(59); err == nil {
		t.Fatal("PresignTTL() 应拒绝过短 TTL")
	}
	if err := storagecontract.ValidateNamespaceName("Bad"); err == nil {
		t.Fatal("ValidateNamespaceName() 应拒绝大写")
	}
}

func TestStrictDecodeRejectsUnknownTopLevelField(t *testing.T) {
	t.Parallel()
	payload := `{"namespaces":{"documents":{"bucket":"bucket","prefix":""}},"principals":{"backend":{"tokens":["12345678901234567890123456789012"],"grants":{"documents":["read"]}}},"unknown":true}`
	if _, err := storagecontract.DecodePolicy(strings.NewReader(payload)); err == nil {
		t.Fatal("DecodePolicy() 应拒绝未知字段")
	}
}
