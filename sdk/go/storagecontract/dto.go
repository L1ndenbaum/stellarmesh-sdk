package storagecontract

import "time"

// ObjectRequest 是 Stat 和 Delete 的请求结构。
type ObjectRequest struct {
	Namespace string `json:"namespace"`
	Key       string `json:"key"`
	VersionID string `json:"version_id,omitempty"`
}

// PresignGetRequest 是下载预签名请求结构。
type PresignGetRequest struct {
	Namespace string `json:"namespace"`
	Key       string `json:"key"`
	VersionID string `json:"version_id,omitempty"`
	ExpiresIn int    `json:"expires_in,omitempty"`
}

// PresignPutRequest 是上传预签名请求结构。
type PresignPutRequest struct {
	Namespace   string            `json:"namespace"`
	Key         string            `json:"key"`
	Size        int64             `json:"size"`
	ContentType string            `json:"content_type,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	Checksum    *Checksum         `json:"checksum,omitempty"`
	ExpiresIn   int               `json:"expires_in,omitempty"`
}

// MultipartCreateRequest 是 Multipart 初始化请求结构。
type MultipartCreateRequest struct {
	Namespace   string            `json:"namespace"`
	Key         string            `json:"key"`
	ContentType string            `json:"content_type,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	Checksum    *Checksum         `json:"checksum,omitempty"`
}

// MultipartPartRequest 是单分片预签名请求结构。
type MultipartPartRequest struct {
	Namespace  string `json:"namespace"`
	Key        string `json:"key"`
	UploadID   string `json:"upload_id"`
	PartNumber int32  `json:"part_number"`
	ExpiresIn  int    `json:"expires_in,omitempty"`
}

// CompletedPart 是已上传分片结构。
type CompletedPart struct {
	PartNumber int32  `json:"part_number"`
	ETag       string `json:"etag"`
}

// MultipartCompleteRequest 是 Multipart 完成请求结构。
type MultipartCompleteRequest struct {
	Namespace string          `json:"namespace"`
	Key       string          `json:"key"`
	UploadID  string          `json:"upload_id"`
	Parts     []CompletedPart `json:"parts"`
}

// MultipartAbortRequest 是 Multipart 中止请求结构。
type MultipartAbortRequest struct {
	Namespace string `json:"namespace"`
	Key       string `json:"key"`
	UploadID  string `json:"upload_id"`
}

// Checksum 是 Base64 编码的单一校验和。
type Checksum struct {
	Algorithm string `json:"algorithm"`
	Value     string `json:"value"`
}

// ObjectInfo 是控制面对象元数据响应。
type ObjectInfo struct {
	Key          string            `json:"key"`
	VersionID    string            `json:"version_id,omitempty"`
	ETag         string            `json:"etag,omitempty"`
	Size         int64             `json:"size"`
	ContentType  string            `json:"content_type,omitempty"`
	LastModified *time.Time        `json:"last_modified,omitempty"`
	Metadata     map[string]string `json:"metadata"`
	Checksum     *Checksum         `json:"checksum,omitempty"`
}

// PresignedRequest 是数据面请求响应。
type PresignedRequest struct {
	Method    string              `json:"method"`
	URL       string              `json:"url"`
	Headers   map[string][]string `json:"headers"`
	ExpiresAt time.Time           `json:"expires_at"`
}

// MultipartUpload 是 Multipart 初始化响应。
type MultipartUpload struct {
	Key      string `json:"key"`
	UploadID string `json:"upload_id"`
}
