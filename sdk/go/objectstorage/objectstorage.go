// Package objectstorage 定义与对象存储厂商无关的公共契约。
package objectstorage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	// MaxObjectKeyBytes 是 S3 物理对象键的最大 UTF-8 字节数。
	MaxObjectKeyBytes = 1024
	// MaxSinglePutBytes 是单次 Put 支持的最大对象大小。
	MaxSinglePutBytes int64 = 5 * 1024 * 1024 * 1024
	// MinPresignTTL 是预签名请求允许的最短有效期。
	MinPresignTTL = time.Minute
	// MaxPresignTTL 是预签名请求允许的最长有效期。
	MaxPresignTTL = time.Hour
)

var (
	ErrNotFound           = errors.New("object storage: not found")
	ErrForbidden          = errors.New("object storage: forbidden")
	ErrPreconditionFailed = errors.New("object storage: precondition failed")
	ErrConflict           = errors.New("object storage: conflict")
	ErrInvalidArgument    = errors.New("object storage: invalid argument")
	ErrUnavailable        = errors.New("object storage: unavailable")
)

// Error 为底层错误补充稳定类别、操作名和逻辑对象键。
type Error struct {
	Kind      error
	Operation string
	Key       string
	Message   string
	Err       error
}

func (e *Error) Error() string {
	if e == nil {
		return "object storage: <nil>"
	}
	message := ""
	if e.Message != "" {
		message = ": " + e.Message
	}
	if e.Key == "" {
		return fmt.Sprintf("object storage %s: %v%s", e.Operation, e.Kind, message)
	}
	return fmt.Sprintf("object storage %s key %q: %v%s", e.Operation, e.Key, e.Kind, message)
}

// Unwrap 同时保留公共错误类别和底层 provider 错误。
func (e *Error) Unwrap() []error {
	if e == nil {
		return nil
	}
	if e.Err == nil || e.Err == e.Kind {
		return []error{e.Kind}
	}
	return []error{e.Kind, e.Err}
}

// Namespace 将一个客户端固定到唯一 Bucket 和可选前缀。
type Namespace struct {
	Bucket string
	Prefix string
}

// NormalizeNamespace 校验 namespace，并将非空前缀规范化为单个结尾斜杠。
func NormalizeNamespace(namespace Namespace) (Namespace, error) {
	if strings.TrimSpace(namespace.Bucket) == "" || namespace.Bucket != strings.TrimSpace(namespace.Bucket) {
		return Namespace{}, invalid("configure", "", "bucket 不能为空或包含首尾空白")
	}
	if !utf8.ValidString(namespace.Bucket) || containsControl(namespace.Bucket) {
		return Namespace{}, invalid("configure", "", "bucket 必须是有效 UTF-8 且不能包含控制字符")
	}
	if strings.HasPrefix(namespace.Prefix, "/") {
		return Namespace{}, invalid("configure", "", "prefix 不能以 / 开头")
	}
	if !utf8.ValidString(namespace.Prefix) || containsControl(namespace.Prefix) {
		return Namespace{}, invalid("configure", "", "prefix 必须是有效 UTF-8 且不能包含控制字符")
	}
	prefix := strings.TrimRight(namespace.Prefix, "/")
	if prefix != "" {
		prefix += "/"
	}
	if len(prefix) > MaxObjectKeyBytes {
		return Namespace{}, invalid("configure", "", "prefix 超过 1024 字节")
	}
	return Namespace{Bucket: namespace.Bucket, Prefix: prefix}, nil
}

// ObjectRef 标识一个逻辑对象及其可选版本。
type ObjectRef struct {
	Key       string
	VersionID string
}

// ByteRange 描述闭区间或从 Start 开始的开放区间。
type ByteRange struct {
	Start int64
	End   *int64
}

// HeaderChecksum 保存 S3 规范要求的 Base64 校验和值。
type HeaderChecksum struct {
	CRC32  string
	CRC32C string
	SHA1   string
	SHA256 string
}

// ObjectInfo 是对象元数据的 provider-neutral 表示。
type ObjectInfo struct {
	Key          string
	VersionID    string
	ETag         string
	Size         int64
	ContentType  string
	LastModified time.Time
	Metadata     map[string]string
	Checksum     HeaderChecksum
}

// Object 包含对象元数据和由调用方负责关闭的响应体。
type Object struct {
	ObjectInfo
	Body io.ReadCloser
}

// PutRequest 描述单次流式上传。
type PutRequest struct {
	Key         string
	Body        io.Reader
	Size        int64
	ContentType string
	Metadata    map[string]string
	Checksum    HeaderChecksum
	IfMatch     string
	IfNoneMatch string
}

// GetRequest 描述读取对象及可选范围。
type GetRequest struct {
	Object ObjectRef
	Range  *ByteRange
}

// DeleteRequest 描述删除对象或指定版本。
type DeleteRequest struct {
	Object  ObjectRef
	IfMatch string
}

// PresignGetRequest 描述下载预签名请求。
type PresignGetRequest struct {
	Object ObjectRef
	Range  *ByteRange
	TTL    time.Duration
}

// PresignPutRequest 描述上传预签名请求。
type PresignPutRequest struct {
	Key         string
	Size        int64
	ContentType string
	Metadata    map[string]string
	Checksum    HeaderChecksum
	IfMatch     string
	IfNoneMatch string
	TTL         time.Duration
}

// PresignedRequest 是客户端执行数据面请求所需的完整签名信息。
type PresignedRequest struct {
	Method    string
	URL       string
	Headers   map[string][]string
	ExpiresAt time.Time
}

// CreateMultipartRequest 描述 Multipart 初始化请求。
type CreateMultipartRequest struct {
	Key         string
	ContentType string
	Metadata    map[string]string
	Checksum    HeaderChecksum
}

// MultipartUpload 标识一条显式 Multipart 生命周期。
type MultipartUpload struct {
	Key      string
	UploadID string
}

// PresignPartRequest 描述单个分片的预签名请求。
type PresignPartRequest struct {
	Key        string
	UploadID   string
	PartNumber int32
	TTL        time.Duration
}

// CompletedPart 是完成 Multipart 所需的分片结果。
type CompletedPart struct {
	PartNumber int32
	ETag       string
}

// CompleteMultipartRequest 描述 Multipart 完成请求。
type CompleteMultipartRequest struct {
	Key         string
	UploadID    string
	Parts       []CompletedPart
	IfMatch     string
	IfNoneMatch string
}

// AbortMultipartRequest 描述 Multipart 中止请求。
type AbortMultipartRequest struct {
	Key      string
	UploadID string
}

type Checker interface {
	Check(context.Context) error
}

type Reader interface {
	Stat(context.Context, ObjectRef) (ObjectInfo, error)
	Get(context.Context, GetRequest) (*Object, error)
}

type Writer interface {
	Put(context.Context, PutRequest) (ObjectInfo, error)
	Delete(context.Context, DeleteRequest) error
}

type Presigner interface {
	PresignGet(context.Context, PresignGetRequest) (PresignedRequest, error)
	PresignPut(context.Context, PresignPutRequest) (PresignedRequest, error)
}

type MultipartStore interface {
	CreateMultipart(context.Context, CreateMultipartRequest) (MultipartUpload, error)
	PresignPart(context.Context, PresignPartRequest) (PresignedRequest, error)
	CompleteMultipart(context.Context, CompleteMultipartRequest) (ObjectInfo, error)
	AbortMultipart(context.Context, AbortMultipartRequest) error
}

// Outcome 表示 Observer 可使用的低基数结果。
type Outcome string

const (
	OutcomeSuccess Outcome = "success"
	OutcomeError   Outcome = "error"
)

// Observation 不含 Bucket、完整对象键或 URL。
type Observation struct {
	Operation string
	Outcome   Outcome
	Duration  time.Duration
	Bytes     int64
	ErrorKind error
}

// Observer 接收一次已完成对象存储操作的有限观测信息。
type Observer interface {
	Observe(context.Context, Observation)
}

// ObserverFunc 允许用函数实现 Observer。
type ObserverFunc func(context.Context, Observation)

func (f ObserverFunc) Observe(ctx context.Context, observation Observation) {
	f(ctx, observation)
}

// ValidateKey 校验逻辑键和 namespace 拼接后的物理键长度。
func ValidateKey(namespace Namespace, key string) error {
	if !utf8.ValidString(key) || key == "" || key != strings.TrimSpace(key) {
		return invalid("validate", key, "key 必须是有效 UTF-8、非空且无首尾空白")
	}
	if strings.HasPrefix(key, "/") || containsControl(key) {
		return invalid("validate", key, "key 不能以 / 开头或包含控制字符")
	}
	if len(namespace.Prefix)+len(key) > MaxObjectKeyBytes {
		return invalid("validate", key, "物理对象键超过 1024 字节")
	}
	return nil
}

// ValidateTTL 解析零值默认值并检查预签名有效期。
func ValidateTTL(ttl, defaultTTL time.Duration) (time.Duration, error) {
	if ttl == 0 {
		ttl = defaultTTL
	}
	if ttl < MinPresignTTL || ttl > MaxPresignTTL {
		return 0, invalid("presign", "", "TTL 必须在 1 分钟到 1 小时之间")
	}
	return ttl, nil
}

func containsControl(value string) bool {
	return strings.IndexFunc(value, unicode.IsControl) >= 0
}

func invalid(operation, key, message string) error {
	return &Error{Kind: ErrInvalidArgument, Operation: operation, Key: key, Message: message, Err: errors.New(message)}
}
