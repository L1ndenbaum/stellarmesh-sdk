package httpapi

import (
	"encoding/base64"
	"strings"

	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/objectstorage"
	"github.com/L1ndenbaum/stellarmesh-sdk/services/storage/internal/storagev1"
)

func parseChecksum(checksum *storagev1.Checksum) (objectstorage.HeaderChecksum, bool) {
	if checksum == nil {
		return objectstorage.HeaderChecksum{}, true
	}
	if checksum.Value == "" {
		return objectstorage.HeaderChecksum{}, false
	}
	if _, err := base64.StdEncoding.DecodeString(checksum.Value); err != nil {
		return objectstorage.HeaderChecksum{}, false
	}
	switch strings.ToUpper(checksum.Algorithm) {
	case "CRC32":
		return objectstorage.HeaderChecksum{CRC32: checksum.Value}, true
	case "CRC32C":
		return objectstorage.HeaderChecksum{CRC32C: checksum.Value}, true
	case "SHA1":
		return objectstorage.HeaderChecksum{SHA1: checksum.Value}, true
	case "SHA256":
		return objectstorage.HeaderChecksum{SHA256: checksum.Value}, true
	default:
		return objectstorage.HeaderChecksum{}, false
	}
}

func objectInfo(info objectstorage.ObjectInfo) storagev1.ObjectInfo {
	result := storagev1.ObjectInfo{
		Key: info.Key, VersionID: info.VersionID, ETag: info.ETag, Size: info.Size,
		ContentType: info.ContentType, Metadata: info.Metadata, Checksum: checksum(info.Checksum),
	}
	if !info.LastModified.IsZero() {
		lastModified := info.LastModified
		result.LastModified = &lastModified
	}
	if result.Metadata == nil {
		result.Metadata = map[string]string{}
	}
	return result
}

func checksum(value objectstorage.HeaderChecksum) *storagev1.Checksum {
	switch {
	case value.CRC32 != "":
		return &storagev1.Checksum{Algorithm: "CRC32", Value: value.CRC32}
	case value.CRC32C != "":
		return &storagev1.Checksum{Algorithm: "CRC32C", Value: value.CRC32C}
	case value.SHA1 != "":
		return &storagev1.Checksum{Algorithm: "SHA1", Value: value.SHA1}
	case value.SHA256 != "":
		return &storagev1.Checksum{Algorithm: "SHA256", Value: value.SHA256}
	default:
		return nil
	}
}

func presignedRequest(request objectstorage.PresignedRequest) storagev1.PresignedRequest {
	return storagev1.PresignedRequest{
		Method: request.Method, URL: request.URL, Headers: request.Headers, ExpiresAt: request.ExpiresAt,
	}
}
