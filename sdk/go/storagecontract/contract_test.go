package storagecontract_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/objectstorage"
	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/storagecontract"
)

func TestValidAccessFixtureAndAuthorization(t *testing.T) {
	t.Parallel()
	payload := readContractFile(t, "testdata", "valid-access-config.json")
	policy, err := storagecontract.DecodePolicy(bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("DecodePolicy() error = %v", err)
	}
	if decision := policy.Authorize("storage-contract-primary-token-00000001", "documents", storagecontract.CapabilityDelete); decision != storagecontract.DecisionAllowed {
		t.Fatalf("delete decision = %v", decision)
	}
	if decision := policy.Authorize("storage-contract-rolling-token-00000002", "images", storagecontract.CapabilityDelete); decision != storagecontract.DecisionForbidden {
		t.Fatalf("forbidden decision = %v", decision)
	}
	if decision := policy.Authorize("unknown", "documents", storagecontract.CapabilityRead); decision != storagecontract.DecisionUnauthenticated {
		t.Fatalf("unknown token decision = %v", decision)
	}
	namespace, exists := policy.Namespace("images")
	if !exists || namespace.Bucket != "project-images" || namespace.Prefix != "originals/" {
		t.Fatalf("Namespace(images) = %+v, %v", namespace, exists)
	}
	namespaces := policy.Namespaces()
	delete(namespaces, "images")
	if _, exists := policy.Namespace("images"); !exists {
		t.Fatal("Namespaces() 泄露内部 map")
	}
	if strings.Contains(strings.ToLower(fmt.Sprintf("%#v", policy)), "storage-contract-primary") {
		t.Fatal("Policy 保留了原始 token")
	}
}

func TestInvalidAccessFixtures(t *testing.T) {
	t.Parallel()
	var fixtures []struct {
		Name    string          `json:"name"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(readContractFile(t, "testdata", "invalid-access-configs.json"), &fixtures); err != nil {
		t.Fatalf("解析 fixture: %v", err)
	}
	for _, fixture := range fixtures {
		fixture := fixture
		t.Run(fixture.Name, func(t *testing.T) {
			t.Parallel()
			if _, err := storagecontract.DecodePolicy(bytes.NewReader(fixture.Payload)); err == nil {
				t.Fatal("DecodePolicy() 应拒绝无效配置")
			}
		})
	}
}

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

func TestRequestLimitsMatchContractFile(t *testing.T) {
	t.Parallel()
	var limits struct {
		MaxControlBodyBytes       int   `json:"max_control_body_bytes"`
		MaxPhysicalKeyBytes       int   `json:"max_physical_key_bytes"`
		MaxMetadataItems          int   `json:"max_metadata_items"`
		MaxMetadataUTF8Bytes      int   `json:"max_metadata_utf8_bytes"`
		MaxContentTypeBytes       int   `json:"max_content_type_bytes"`
		MinTokenUnicodeCharacters int   `json:"min_token_unicode_characters"`
		DefaultPresignTTLSeconds  int   `json:"default_presign_ttl_seconds"`
		MinPresignTTLSeconds      int   `json:"min_presign_ttl_seconds"`
		MaxPresignTTLSeconds      int   `json:"max_presign_ttl_seconds"`
		MinMultipartPartNumber    int   `json:"min_multipart_part_number"`
		MaxMultipartPartNumber    int   `json:"max_multipart_part_number"`
		MaxSinglePutBytes         int64 `json:"max_single_put_bytes"`
	}
	if err := json.Unmarshal(readContractFile(t, "limits.json"), &limits); err != nil {
		t.Fatalf("解析 limits: %v", err)
	}
	if limits.MaxControlBodyBytes != storagecontract.MaxControlBodyBytes ||
		limits.MaxPhysicalKeyBytes != objectstorage.MaxObjectKeyBytes ||
		limits.MaxMetadataItems != storagecontract.MaxMetadataItems ||
		limits.MaxMetadataUTF8Bytes != storagecontract.MaxMetadataBytes ||
		limits.MaxContentTypeBytes != storagecontract.MaxContentTypeBytes ||
		limits.MinTokenUnicodeCharacters != storagecontract.MinTokenRunes ||
		limits.DefaultPresignTTLSeconds != storagecontract.DefaultPresignTTLSeconds ||
		limits.MinPresignTTLSeconds != storagecontract.MinPresignTTLSeconds ||
		limits.MaxPresignTTLSeconds != storagecontract.MaxPresignTTLSeconds ||
		limits.MinMultipartPartNumber != storagecontract.MinPartNumber ||
		limits.MaxMultipartPartNumber != storagecontract.MaxPartNumber ||
		limits.MaxSinglePutBytes != objectstorage.MaxSinglePutBytes {
		t.Fatalf("Go 常量与 limits.json 不一致: %+v", limits)
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

func readContractFile(t *testing.T, parts ...string) []byte {
	t.Helper()
	pathParts := append([]string{"..", "..", "..", "contracts", "storage", "v1"}, parts...)
	payload, err := os.ReadFile(filepath.Join(pathParts...))
	if err != nil {
		t.Fatalf("读取契约文件: %v", err)
	}
	return payload
}
