package s3store

import (
	"context"
	"net/http"
	"time"

	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/objectstorage"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

type s3API interface {
	HeadBucket(context.Context, *s3.HeadBucketInput, ...func(*s3.Options)) (*s3.HeadBucketOutput, error)
	HeadObject(context.Context, *s3.HeadObjectInput, ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
	GetObject(context.Context, *s3.GetObjectInput, ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	PutObject(context.Context, *s3.PutObjectInput, ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	DeleteObject(context.Context, *s3.DeleteObjectInput, ...func(*s3.Options)) (*s3.DeleteObjectOutput, error)
	CreateMultipartUpload(context.Context, *s3.CreateMultipartUploadInput, ...func(*s3.Options)) (*s3.CreateMultipartUploadOutput, error)
	CompleteMultipartUpload(context.Context, *s3.CompleteMultipartUploadInput, ...func(*s3.Options)) (*s3.CompleteMultipartUploadOutput, error)
	AbortMultipartUpload(context.Context, *s3.AbortMultipartUploadInput, ...func(*s3.Options)) (*s3.AbortMultipartUploadOutput, error)
}

type presignAPI interface {
	PresignGetObject(context.Context, *s3.GetObjectInput, ...func(*s3.PresignOptions)) (*v4.PresignedHTTPRequest, error)
	PresignPutObject(context.Context, *s3.PutObjectInput, ...func(*s3.PresignOptions)) (*v4.PresignedHTTPRequest, error)
	PresignUploadPart(context.Context, *s3.UploadPartInput, ...func(*s3.PresignOptions)) (*v4.PresignedHTTPRequest, error)
}

// Client 实现 objectstorage 的全部小接口。
type Client struct {
	config    Config
	api       s3API
	presigner presignAPI
	observer  objectstorage.Observer
	now       func() time.Time
}

func newClient(config Config, api s3API, presigner presignAPI, observer objectstorage.Observer) *Client {
	return &Client{config: config, api: api, presigner: presigner, observer: observer, now: time.Now}
}

func (c *Client) physicalKey(key string) string {
	return c.config.Namespace.Prefix + key
}

func (c *Client) observe(ctx context.Context, started time.Time, operation string, bytes int64, err error) {
	if c.observer == nil {
		return
	}
	observation := objectstorage.Observation{
		Operation: operation,
		Outcome:   objectstorage.OutcomeSuccess,
		Duration:  c.now().Sub(started),
		Bytes:     bytes,
	}
	if err != nil {
		observation.Outcome = objectstorage.OutcomeError
		observation.ErrorKind = errorKind(err)
	}
	c.observer.Observe(ctx, observation)
}

func copyHeaders(headers http.Header) map[string][]string {
	result := make(map[string][]string, len(headers))
	for key, values := range headers {
		result[key] = append([]string(nil), values...)
	}
	return result
}

func completedParts(parts []objectstorage.CompletedPart) []types.CompletedPart {
	result := make([]types.CompletedPart, len(parts))
	for index, part := range parts {
		result[index] = types.CompletedPart{ETag: stringPointer(part.ETag), PartNumber: int32Pointer(part.PartNumber)}
	}
	return result
}

func stringPointer(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func int32Pointer(value int32) *int32 {
	return &value
}

var (
	_ objectstorage.Checker        = (*Client)(nil)
	_ objectstorage.Reader         = (*Client)(nil)
	_ objectstorage.Writer         = (*Client)(nil)
	_ objectstorage.Presigner      = (*Client)(nil)
	_ objectstorage.MultipartStore = (*Client)(nil)
)
