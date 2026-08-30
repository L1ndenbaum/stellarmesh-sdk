package storagev1_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/objectstorage"
	"github.com/L1ndenbaum/stellarmesh-sdk/services/storage/internal/storagev1"
)

func TestGoStoragePolicyAcceptsSharedAccessFixture(t *testing.T) {
	policy, err := storagev1.DecodePolicy(bytes.NewReader(readStorageContract(t, "testdata", "valid-access-config.json")))
	if err != nil {
		t.Fatalf("DecodePolicy() error = %v", err)
	}
	if decision := policy.Authorize("storage-contract-primary-token-00000001", "documents", storagev1.CapabilityDelete); decision != storagev1.DecisionAllowed {
		t.Fatalf("delete decision = %v", decision)
	}
	if decision := policy.Authorize("storage-contract-rolling-token-00000002", "images", storagev1.CapabilityDelete); decision != storagev1.DecisionForbidden {
		t.Fatalf("forbidden decision = %v", decision)
	}
	if decision := policy.Authorize("unknown", "documents", storagev1.CapabilityRead); decision != storagev1.DecisionUnauthenticated {
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

func TestGoStoragePolicyRejectsSharedInvalidFixtures(t *testing.T) {
	var fixtures []struct {
		Name    string          `json:"name"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(readStorageContract(t, "testdata", "invalid-access-configs.json"), &fixtures); err != nil {
		t.Fatalf("解析 fixture: %v", err)
	}
	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			if _, err := storagev1.DecodePolicy(bytes.NewReader(fixture.Payload)); err == nil {
				t.Fatal("DecodePolicy() 应拒绝无效配置")
			}
		})
	}
}

func TestGoStorageLimitsMatchSharedContract(t *testing.T) {
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
	if err := json.Unmarshal(readStorageContract(t, "limits.json"), &limits); err != nil {
		t.Fatalf("解析 limits: %v", err)
	}
	if limits.MaxControlBodyBytes != storagev1.MaxControlBodyBytes ||
		limits.MaxPhysicalKeyBytes != objectstorage.MaxObjectKeyBytes ||
		limits.MaxMetadataItems != storagev1.MaxMetadataItems ||
		limits.MaxMetadataUTF8Bytes != storagev1.MaxMetadataBytes ||
		limits.MaxContentTypeBytes != storagev1.MaxContentTypeBytes ||
		limits.MinTokenUnicodeCharacters != storagev1.MinTokenRunes ||
		limits.DefaultPresignTTLSeconds != storagev1.DefaultPresignTTLSeconds ||
		limits.MinPresignTTLSeconds != storagev1.MinPresignTTLSeconds ||
		limits.MaxPresignTTLSeconds != storagev1.MaxPresignTTLSeconds ||
		limits.MinMultipartPartNumber != storagev1.MinPartNumber ||
		limits.MaxMultipartPartNumber != storagev1.MaxPartNumber ||
		limits.MaxSinglePutBytes != objectstorage.MaxSinglePutBytes {
		t.Fatalf("Go 常量与 limits.json 不一致: %+v", limits)
	}
}

func readStorageContract(t *testing.T, parts ...string) []byte {
	t.Helper()
	pathParts := append([]string{"..", "..", "..", "..", "contracts", "storage", "v1"}, parts...)
	payload, err := os.ReadFile(filepath.Join(pathParts...))
	if err != nil {
		t.Fatalf("读取契约文件: %v", err)
	}
	return payload
}
