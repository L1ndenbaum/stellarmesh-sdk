package s3store

import (
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/objectstorage"
)

func (c *Client) validateRef(operation string, ref objectstorage.ObjectRef) error {
	if err := objectstorage.ValidateKey(c.config.Namespace, ref.Key); err != nil {
		return wrapInvalid(operation, ref.Key, err)
	}
	if ref.VersionID != "" && (!utf8.ValidString(ref.VersionID) || strings.IndexFunc(ref.VersionID, unicode.IsControl) >= 0) {
		return invalid(operation, ref.Key, "version ID 必须是有效 UTF-8 且不能包含控制字符")
	}
	return nil
}

func validateRange(operation, key string, value *objectstorage.ByteRange) error {
	if value == nil {
		return nil
	}
	if value.Start < 0 {
		return invalid(operation, key, "range start 不能小于 0")
	}
	if value.End != nil && *value.End < value.Start {
		return invalid(operation, key, "range end 不能小于 start")
	}
	return nil
}

func formatRange(value *objectstorage.ByteRange) *string {
	if value == nil {
		return nil
	}
	formatted := fmt.Sprintf("bytes=%d-", value.Start)
	if value.End != nil {
		formatted += fmt.Sprintf("%d", *value.End)
	}
	return &formatted
}

func validatePutRequest(operation string, namespace objectstorage.Namespace, request objectstorage.PutRequest) error {
	if err := objectstorage.ValidateKey(namespace, request.Key); err != nil {
		return wrapInvalid(operation, request.Key, err)
	}
	if request.Body == nil {
		return invalid(operation, request.Key, "body 不能为 nil")
	}
	if request.Size < 0 {
		return invalid(operation, request.Key, "size 不能小于 0")
	}
	if request.Size > objectstorage.MaxSinglePutBytes {
		return invalid(operation, request.Key, "单次 Put 不能超过 5 GiB，请使用 Multipart")
	}
	return validateUploadFields(operation, request.Key, request.ContentType, request.Metadata, request.Checksum)
}

func validatePresignPutRequest(namespace objectstorage.Namespace, request objectstorage.PresignPutRequest) error {
	if err := objectstorage.ValidateKey(namespace, request.Key); err != nil {
		return wrapInvalid("presign_put", request.Key, err)
	}
	if request.Size < 0 || request.Size > objectstorage.MaxSinglePutBytes {
		return invalid("presign_put", request.Key, "size 必须在 0 到 5 GiB 之间")
	}
	return validateUploadFields("presign_put", request.Key, request.ContentType, request.Metadata, request.Checksum)
}

func validateUploadFields(operation, key, contentType string, metadata map[string]string, checksum objectstorage.HeaderChecksum) error {
	if !utf8.ValidString(contentType) || strings.IndexFunc(contentType, unicode.IsControl) >= 0 {
		return invalid(operation, key, "content type 必须是有效 UTF-8 且不能包含控制字符")
	}
	for metadataKey, value := range metadata {
		if metadataKey == "" || !utf8.ValidString(metadataKey) || !utf8.ValidString(value) || strings.IndexFunc(metadataKey+value, unicode.IsControl) >= 0 {
			return invalid(operation, key, "metadata 必须是有效 UTF-8、key 非空且不能包含控制字符")
		}
	}
	return validateChecksum(operation, key, checksum)
}

func validateChecksum(operation, key string, checksum objectstorage.HeaderChecksum) error {
	values := []string{checksum.CRC32, checksum.CRC32C, checksum.SHA1, checksum.SHA256}
	count := 0
	for _, value := range values {
		if value == "" {
			continue
		}
		count++
		if _, err := base64.StdEncoding.DecodeString(value); err != nil {
			return invalid(operation, key, "checksum 必须使用标准 Base64 编码")
		}
	}
	if count > 1 {
		return invalid(operation, key, "一次请求只能设置一种 checksum")
	}
	return nil
}

func (c *Client) ttl(value time.Duration, operation, key string) (time.Duration, error) {
	ttl, err := objectstorage.ValidateTTL(value, c.config.DefaultPresignTTL)
	if err != nil {
		return 0, wrapInvalid(operation, key, err)
	}
	if ttl > c.config.MaxPresignTTL {
		return 0, invalid(operation, key, "TTL 超过客户端配置的最大值")
	}
	return ttl, nil
}

func validateUploadID(operation, key, uploadID string) error {
	if uploadID == "" || !utf8.ValidString(uploadID) || strings.IndexFunc(uploadID, unicode.IsControl) >= 0 {
		return invalid(operation, key, "upload ID 不能为空、必须是有效 UTF-8 且不能包含控制字符")
	}
	return nil
}

func validatePartNumber(operation, key string, partNumber int32) error {
	if partNumber < 1 || partNumber > 10000 {
		return invalid(operation, key, "part number 必须在 1 到 10000 之间")
	}
	return nil
}

func validateAndSortParts(operation, key string, parts []objectstorage.CompletedPart) ([]objectstorage.CompletedPart, error) {
	if len(parts) == 0 {
		return nil, invalid(operation, key, "parts 不能为空")
	}
	result := append([]objectstorage.CompletedPart(nil), parts...)
	seen := make(map[int32]struct{}, len(result))
	for _, part := range result {
		if err := validatePartNumber(operation, key, part.PartNumber); err != nil {
			return nil, err
		}
		if strings.TrimSpace(part.ETag) == "" {
			return nil, invalid(operation, key, "part ETag 不能为空")
		}
		if _, exists := seen[part.PartNumber]; exists {
			return nil, invalid(operation, key, "part number 不能重复")
		}
		seen[part.PartNumber] = struct{}{}
	}
	sort.Slice(result, func(left, right int) bool { return result[left].PartNumber < result[right].PartNumber })
	return result, nil
}

func invalid(operation, key, reason string) error {
	return &objectstorage.Error{Kind: objectstorage.ErrInvalidArgument, Operation: operation, Key: key, Message: reason, Err: errors.New(reason)}
}

func wrapInvalid(operation, key string, err error) error {
	return &objectstorage.Error{Kind: objectstorage.ErrInvalidArgument, Operation: operation, Key: key, Err: err}
}
