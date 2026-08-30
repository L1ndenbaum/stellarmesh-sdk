package storagev1

import (
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/objectstorage"
)

const (
	MaxControlBodyBytes      = 64 * 1024
	MaxMetadataItems         = 32
	MaxMetadataBytes         = 2 * 1024
	MaxContentTypeBytes      = 255
	MinTokenRunes            = 32
	DefaultPresignTTLSeconds = 900
	MinPresignTTLSeconds     = 60
	MaxPresignTTLSeconds     = 3600
	MinPartNumber            = 1
	MaxPartNumber            = 10000
)

const ServiceTokenHeader = "X-Storage-Service-Token"

var namespacePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

// ValidateNamespaceName 校验 HTTP 请求中的逻辑 namespace。
func ValidateNamespaceName(name string) error {
	if !namespacePattern.MatchString(name) {
		return errors.New("namespace 不符合命名规则")
	}
	return nil
}

// ValidateObjectInput 校验所有控制面对象请求共享的字段。
func ValidateObjectInput(namespace objectstorage.Namespace, key, versionID, contentType string, metadata map[string]string) error {
	if err := objectstorage.ValidateKey(namespace, key); err != nil {
		return err
	}
	if versionID != "" && (!utf8.ValidString(versionID) || containsControl(versionID)) {
		return errors.New("version_id 必须是有效 UTF-8 且不能包含控制字符")
	}
	if len(contentType) > MaxContentTypeBytes || !utf8.ValidString(contentType) || containsControl(contentType) {
		return errors.New("content_type 超过限制或包含控制字符")
	}
	if len(metadata) > MaxMetadataItems {
		return errors.New("metadata 项数超过限制")
	}
	metadataBytes := 0
	for key, value := range metadata {
		if key == "" || !utf8.ValidString(key) || !utf8.ValidString(value) || containsControl(key) || containsControl(value) {
			return errors.New("metadata 必须是有效 UTF-8、key 非空且不能包含控制字符")
		}
		metadataBytes += len(key) + len(value)
	}
	if metadataBytes > MaxMetadataBytes {
		return errors.New("metadata 总字节数超过限制")
	}
	return nil
}

// PresignTTL 将秒数转换为契约允许的有效期。
func PresignTTL(seconds int) (time.Duration, error) {
	if seconds == 0 {
		seconds = DefaultPresignTTLSeconds
	}
	if seconds < MinPresignTTLSeconds || seconds > MaxPresignTTLSeconds {
		return 0, errors.New("expires_in 必须在 60 到 3600 秒之间")
	}
	return time.Duration(seconds) * time.Second, nil
}

func containsControl(value string) bool {
	return strings.IndexFunc(value, unicode.IsControl) >= 0
}
