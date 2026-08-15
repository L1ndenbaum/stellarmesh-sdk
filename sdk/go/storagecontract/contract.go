// Package storagecontract 定义 Storage v1 HTTP 契约和访问策略。
package storagecontract

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

// Capability 是项目 principal 可被授予的固定能力。
type Capability string

const (
	CapabilityRead   Capability = "read"
	CapabilityWrite  Capability = "write"
	CapabilityDelete Capability = "delete"
)

// Decision 区分未认证、已认证但无权和允许。
type Decision uint8

const (
	DecisionUnauthenticated Decision = iota
	DecisionForbidden
	DecisionAllowed
)

// AccessConfig 是只读项目访问文件的严格 JSON 表示。
type AccessConfig struct {
	Namespaces map[string]NamespaceConfig `json:"namespaces"`
	Principals map[string]PrincipalConfig `json:"principals"`
}

// NamespaceConfig 将逻辑 namespace 绑定到物理 Bucket 与前缀。
type NamespaceConfig struct {
	Bucket string `json:"bucket"`
	Prefix string `json:"prefix"`
}

// PrincipalConfig 定义轮换 token 和 namespace grants。
type PrincipalConfig struct {
	Tokens []string                `json:"tokens"`
	Grants map[string][]Capability `json:"grants"`
}

type compiledPrincipal struct {
	tokens [][sha256.Size]byte
	grants map[string]map[Capability]struct{}
}

// Policy 只保存 token digest 和已验证授权，不保留原始 token。
type Policy struct {
	namespaces map[string]objectstorage.Namespace
	principals []compiledPrincipal
}

// DecodePolicy 严格解码并编译访问配置。
func DecodePolicy(reader io.Reader) (*Policy, error) {
	decoder := json.NewDecoder(io.LimitReader(reader, 1<<20))
	decoder.DisallowUnknownFields()
	var config AccessConfig
	if err := decoder.Decode(&config); err != nil {
		return nil, fmt.Errorf("解析访问配置: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("访问配置只能包含一个 JSON 对象")
		}
		return nil, fmt.Errorf("解析访问配置尾部: %w", err)
	}
	return CompilePolicy(config)
}

// CompilePolicy 校验原始配置并构造仅保留 digest 的策略。
func CompilePolicy(config AccessConfig) (*Policy, error) {
	if len(config.Namespaces) == 0 {
		return nil, errors.New("namespaces 不能为空")
	}
	if len(config.Principals) == 0 {
		return nil, errors.New("principals 不能为空")
	}
	policy := &Policy{namespaces: make(map[string]objectstorage.Namespace, len(config.Namespaces))}
	for name, namespaceConfig := range config.Namespaces {
		if !namespacePattern.MatchString(name) {
			return nil, fmt.Errorf("namespace %q 不符合命名规则", name)
		}
		namespace, err := objectstorage.NormalizeNamespace(objectstorage.Namespace{Bucket: namespaceConfig.Bucket, Prefix: namespaceConfig.Prefix})
		if err != nil {
			return nil, fmt.Errorf("namespace %q: %w", name, err)
		}
		policy.namespaces[name] = namespace
	}

	seenTokens := make(map[[sha256.Size]byte]struct{})
	for name, principalConfig := range config.Principals {
		if strings.TrimSpace(name) == "" || name != strings.TrimSpace(name) || !utf8.ValidString(name) || containsControl(name) {
			return nil, errors.New("principal 名称不能为空、包含首尾空白或控制字符")
		}
		if len(principalConfig.Tokens) == 0 {
			return nil, fmt.Errorf("principal %q 的 tokens 不能为空", name)
		}
		if len(principalConfig.Grants) == 0 {
			return nil, fmt.Errorf("principal %q 的 grants 不能为空", name)
		}
		compiled := compiledPrincipal{grants: make(map[string]map[Capability]struct{}, len(principalConfig.Grants))}
		for _, token := range principalConfig.Tokens {
			if !utf8.ValidString(token) || utf8.RuneCountInString(token) < MinTokenRunes || containsControl(token) {
				return nil, fmt.Errorf("principal %q 的 token 至少需要 32 个 Unicode 字符且不能包含控制字符", name)
			}
			digest := sha256.Sum256([]byte(token))
			if _, exists := seenTokens[digest]; exists {
				return nil, errors.New("访问配置包含重复 token")
			}
			seenTokens[digest] = struct{}{}
			compiled.tokens = append(compiled.tokens, digest)
		}
		for namespace, capabilities := range principalConfig.Grants {
			if _, exists := policy.namespaces[namespace]; !exists {
				return nil, fmt.Errorf("principal %q 引用了未知 namespace %q", name, namespace)
			}
			if len(capabilities) == 0 {
				return nil, fmt.Errorf("principal %q 对 namespace %q 的 capability 不能为空", name, namespace)
			}
			capabilitySet := make(map[Capability]struct{}, len(capabilities))
			for _, capability := range capabilities {
				switch capability {
				case CapabilityRead, CapabilityWrite, CapabilityDelete:
				default:
					return nil, fmt.Errorf("principal %q 包含未知 capability %q", name, capability)
				}
				if _, duplicate := capabilitySet[capability]; duplicate {
					return nil, fmt.Errorf("principal %q 对 namespace %q 包含重复 capability %q", name, namespace, capability)
				}
				capabilitySet[capability] = struct{}{}
			}
			compiled.grants[namespace] = capabilitySet
		}
		policy.principals = append(policy.principals, compiled)
	}
	return policy, nil
}

// Authorize 使用常量时间 digest 比较进行认证和授权。
func (p *Policy) Authorize(token, namespace string, capability Capability) Decision {
	matched := p.authenticate(token)
	if matched < 0 {
		return DecisionUnauthenticated
	}
	capabilities, exists := p.principals[matched].grants[namespace]
	if !exists {
		return DecisionForbidden
	}
	if _, exists := capabilities[capability]; !exists {
		return DecisionForbidden
	}
	return DecisionAllowed
}

// Authenticate 仅校验 token 是否属于已声明 principal。
func (p *Policy) Authenticate(token string) bool {
	return p.authenticate(token) >= 0
}

func (p *Policy) authenticate(token string) int {
	if p == nil || token == "" {
		return -1
	}
	digest := sha256.Sum256([]byte(token))
	matched := -1
	for principalIndex, principal := range p.principals {
		for _, candidate := range principal.tokens {
			if subtle.ConstantTimeCompare(digest[:], candidate[:]) == 1 {
				matched = principalIndex
			}
		}
	}
	return matched
}

// Namespace 返回逻辑 namespace 的只读副本。
func (p *Policy) Namespace(name string) (objectstorage.Namespace, bool) {
	if p == nil {
		return objectstorage.Namespace{}, false
	}
	namespace, exists := p.namespaces[name]
	return namespace, exists
}

// Namespaces 返回全部 namespace 的副本。
func (p *Policy) Namespaces() map[string]objectstorage.Namespace {
	result := make(map[string]objectstorage.Namespace, len(p.namespaces))
	for name, namespace := range p.namespaces {
		result[name] = namespace
	}
	return result
}

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
