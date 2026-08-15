package s3store

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/objectstorage"
	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

type fakeS3 struct {
	headBucket        func(context.Context, *s3.HeadBucketInput) (*s3.HeadBucketOutput, error)
	headObject        func(context.Context, *s3.HeadObjectInput) (*s3.HeadObjectOutput, error)
	getObject         func(context.Context, *s3.GetObjectInput) (*s3.GetObjectOutput, error)
	putObject         func(context.Context, *s3.PutObjectInput) (*s3.PutObjectOutput, error)
	deleteObject      func(context.Context, *s3.DeleteObjectInput) (*s3.DeleteObjectOutput, error)
	createMultipart   func(context.Context, *s3.CreateMultipartUploadInput) (*s3.CreateMultipartUploadOutput, error)
	completeMultipart func(context.Context, *s3.CompleteMultipartUploadInput) (*s3.CompleteMultipartUploadOutput, error)
	abortMultipart    func(context.Context, *s3.AbortMultipartUploadInput) (*s3.AbortMultipartUploadOutput, error)
}

func (f *fakeS3) HeadBucket(ctx context.Context, input *s3.HeadBucketInput, _ ...func(*s3.Options)) (*s3.HeadBucketOutput, error) {
	if f.headBucket != nil {
		return f.headBucket(ctx, input)
	}
	return &s3.HeadBucketOutput{}, nil
}
func (f *fakeS3) HeadObject(ctx context.Context, input *s3.HeadObjectInput, _ ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
	if f.headObject != nil {
		return f.headObject(ctx, input)
	}
	return &s3.HeadObjectOutput{}, nil
}
func (f *fakeS3) GetObject(ctx context.Context, input *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	if f.getObject != nil {
		return f.getObject(ctx, input)
	}
	return &s3.GetObjectOutput{}, nil
}
func (f *fakeS3) PutObject(ctx context.Context, input *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	if f.putObject != nil {
		return f.putObject(ctx, input)
	}
	return &s3.PutObjectOutput{}, nil
}
func (f *fakeS3) DeleteObject(ctx context.Context, input *s3.DeleteObjectInput, _ ...func(*s3.Options)) (*s3.DeleteObjectOutput, error) {
	if f.deleteObject != nil {
		return f.deleteObject(ctx, input)
	}
	return &s3.DeleteObjectOutput{}, nil
}
func (f *fakeS3) CreateMultipartUpload(ctx context.Context, input *s3.CreateMultipartUploadInput, _ ...func(*s3.Options)) (*s3.CreateMultipartUploadOutput, error) {
	if f.createMultipart != nil {
		return f.createMultipart(ctx, input)
	}
	return &s3.CreateMultipartUploadOutput{}, nil
}
func (f *fakeS3) CompleteMultipartUpload(ctx context.Context, input *s3.CompleteMultipartUploadInput, _ ...func(*s3.Options)) (*s3.CompleteMultipartUploadOutput, error) {
	if f.completeMultipart != nil {
		return f.completeMultipart(ctx, input)
	}
	return &s3.CompleteMultipartUploadOutput{}, nil
}
func (f *fakeS3) AbortMultipartUpload(ctx context.Context, input *s3.AbortMultipartUploadInput, _ ...func(*s3.Options)) (*s3.AbortMultipartUploadOutput, error) {
	if f.abortMultipart != nil {
		return f.abortMultipart(ctx, input)
	}
	return &s3.AbortMultipartUploadOutput{}, nil
}

type fakePresigner struct {
	get  func(context.Context, *s3.GetObjectInput, ...func(*s3.PresignOptions)) (*v4.PresignedHTTPRequest, error)
	put  func(context.Context, *s3.PutObjectInput, ...func(*s3.PresignOptions)) (*v4.PresignedHTTPRequest, error)
	part func(context.Context, *s3.UploadPartInput, ...func(*s3.PresignOptions)) (*v4.PresignedHTTPRequest, error)
}

func (f *fakePresigner) PresignGetObject(ctx context.Context, input *s3.GetObjectInput, options ...func(*s3.PresignOptions)) (*v4.PresignedHTTPRequest, error) {
	return f.get(ctx, input, options...)
}
func (f *fakePresigner) PresignPutObject(ctx context.Context, input *s3.PutObjectInput, options ...func(*s3.PresignOptions)) (*v4.PresignedHTTPRequest, error) {
	return f.put(ctx, input, options...)
}
func (f *fakePresigner) PresignUploadPart(ctx context.Context, input *s3.UploadPartInput, options ...func(*s3.PresignOptions)) (*v4.PresignedHTTPRequest, error) {
	return f.part(ctx, input, options...)
}

func testConfig() Config {
	return Config{Region: "test-1", Namespace: objectstorage.Namespace{Bucket: "bucket", Prefix: "tenant/"}, DefaultPresignTTL: 15 * time.Minute, MaxPresignTTL: time.Hour}
}

func TestPutGetAndStatMapping(t *testing.T) {
	t.Parallel()
	contextKey := struct{}{}
	body := &trackingBody{Reader: strings.NewReader("payload")}
	fake := &fakeS3{}
	fake.putObject = func(ctx context.Context, input *s3.PutObjectInput) (*s3.PutObjectOutput, error) {
		if ctx.Value(contextKey) != "value" {
			t.Fatal("context 未透传")
		}
		if aws.ToString(input.Key) != "tenant/a//../b" || aws.ToInt64(input.ContentLength) != 7 {
			t.Fatalf("Put input = %+v", input)
		}
		if input.Metadata["source"] != "test" || aws.ToString(input.ChecksumSHA256) != "YWJj" {
			t.Fatalf("Put fields = %+v", input)
		}
		return &s3.PutObjectOutput{ETag: aws.String("opaque"), VersionId: aws.String("v1")}, nil
	}
	fake.getObject = func(_ context.Context, input *s3.GetObjectInput) (*s3.GetObjectOutput, error) {
		if aws.ToString(input.Range) != "bytes=2-5" || aws.ToString(input.VersionId) != "v1" {
			t.Fatalf("Get input = %+v", input)
		}
		return &s3.GetObjectOutput{Body: body, ContentLength: aws.Int64(4), ETag: aws.String("opaque")}, nil
	}
	fake.headObject = func(_ context.Context, input *s3.HeadObjectInput) (*s3.HeadObjectOutput, error) {
		if aws.ToString(input.Key) != "tenant/a//../b" {
			t.Fatalf("Head key = %q", aws.ToString(input.Key))
		}
		return &s3.HeadObjectOutput{ContentLength: aws.Int64(7), ContentType: aws.String("text/plain"), Metadata: map[string]string{"source": "test"}}, nil
	}
	client := newClient(testConfig(), fake, nil, nil)
	ctx := context.WithValue(context.Background(), contextKey, "value")
	putInfo, err := client.Put(ctx, objectstorage.PutRequest{Key: "a//../b", Body: strings.NewReader("payload"), Size: 7, Metadata: map[string]string{"source": "test"}, Checksum: objectstorage.HeaderChecksum{SHA256: "YWJj"}})
	if err != nil || putInfo.ETag != "opaque" || putInfo.Size != 7 {
		t.Fatalf("Put() = %+v, %v", putInfo, err)
	}
	end := int64(5)
	object, err := client.Get(ctx, objectstorage.GetRequest{Object: objectstorage.ObjectRef{Key: "a//../b", VersionID: "v1"}, Range: &objectstorage.ByteRange{Start: 2, End: &end}})
	if err != nil || object.Size != 4 {
		t.Fatalf("Get() = %+v, %v", object, err)
	}
	if err := object.Body.Close(); err != nil || !body.closed {
		t.Fatal("调用方无法关闭响应体")
	}
	stat, err := client.Stat(ctx, objectstorage.ObjectRef{Key: "a//../b"})
	if err != nil || stat.Size != 7 || stat.ContentType != "text/plain" {
		t.Fatalf("Stat() = %+v, %v", stat, err)
	}
}

func TestPutRejectsInvalidBodyAndLargeObject(t *testing.T) {
	t.Parallel()
	client := newClient(testConfig(), &fakeS3{}, nil, nil)
	requests := []objectstorage.PutRequest{
		{Key: "key", Size: 1},
		{Key: "key", Body: bytes.NewReader(nil), Size: objectstorage.MaxSinglePutBytes + 1},
		{Key: "key", Body: bytes.NewReader(nil), Size: 0, Checksum: objectstorage.HeaderChecksum{SHA1: "not-base64"}},
	}
	for _, request := range requests {
		if _, err := client.Put(context.Background(), request); !errors.Is(err, objectstorage.ErrInvalidArgument) {
			t.Fatalf("Put(%+v) error = %v", request, err)
		}
	}
}

func TestPresignAndMultipartValidation(t *testing.T) {
	t.Parallel()
	var completeInput *s3.CompleteMultipartUploadInput
	fake := &fakeS3{completeMultipart: func(_ context.Context, input *s3.CompleteMultipartUploadInput) (*s3.CompleteMultipartUploadOutput, error) {
		completeInput = input
		return &s3.CompleteMultipartUploadOutput{ETag: aws.String("done")}, nil
	}}
	presigner := &fakePresigner{}
	presigner.get = func(_ context.Context, input *s3.GetObjectInput, options ...func(*s3.PresignOptions)) (*v4.PresignedHTTPRequest, error) {
		configured := s3.PresignOptions{}
		for _, apply := range options {
			apply(&configured)
		}
		if configured.Expires != 10*time.Minute || aws.ToString(input.Key) != "tenant/key" {
			t.Fatalf("presign get input/options = %+v, %+v", input, configured)
		}
		return &v4.PresignedHTTPRequest{Method: http.MethodGet, URL: "https://public.example/key?signature=secret", SignedHeader: http.Header{"X-Signed": []string{"yes"}}}, nil
	}
	presigner.part = func(_ context.Context, input *s3.UploadPartInput, _ ...func(*s3.PresignOptions)) (*v4.PresignedHTTPRequest, error) {
		if aws.ToInt32(input.PartNumber) != 10000 {
			t.Fatalf("part number = %d", aws.ToInt32(input.PartNumber))
		}
		return &v4.PresignedHTTPRequest{Method: http.MethodPut, URL: "https://public.example/part", SignedHeader: http.Header{}}, nil
	}
	client := newClient(testConfig(), fake, presigner, nil)
	request, err := client.PresignGet(context.Background(), objectstorage.PresignGetRequest{Object: objectstorage.ObjectRef{Key: "key"}, TTL: 10 * time.Minute})
	if err != nil || request.Method != http.MethodGet || request.Headers["X-Signed"][0] != "yes" {
		t.Fatalf("PresignGet() = %+v, %v", request, err)
	}
	if _, err := client.PresignPart(context.Background(), objectstorage.PresignPartRequest{Key: "key", UploadID: "upload", PartNumber: 10000}); err != nil {
		t.Fatalf("PresignPart() error = %v", err)
	}
	parts := []objectstorage.CompletedPart{{PartNumber: 2, ETag: "two"}, {PartNumber: 1, ETag: "one"}}
	original := append([]objectstorage.CompletedPart(nil), parts...)
	if _, err := client.CompleteMultipart(context.Background(), objectstorage.CompleteMultipartRequest{Key: "key", UploadID: "upload", Parts: parts}); err != nil {
		t.Fatalf("CompleteMultipart() error = %v", err)
	}
	if !reflect.DeepEqual(parts, original) {
		t.Fatalf("调用方 parts 被修改: %+v", parts)
	}
	if aws.ToInt32(completeInput.MultipartUpload.Parts[0].PartNumber) != 1 {
		t.Fatalf("parts 未排序: %+v", completeInput.MultipartUpload.Parts)
	}
	_, err = client.CompleteMultipart(context.Background(), objectstorage.CompleteMultipartRequest{Key: "key", UploadID: "upload", Parts: []objectstorage.CompletedPart{{PartNumber: 1, ETag: "one"}, {PartNumber: 1, ETag: "duplicate"}}})
	if !errors.Is(err, objectstorage.ErrInvalidArgument) {
		t.Fatalf("duplicate parts error = %v", err)
	}
}

func TestErrorMappingAndObserver(t *testing.T) {
	t.Parallel()
	var observation objectstorage.Observation
	fake := &fakeS3{headObject: func(context.Context, *s3.HeadObjectInput) (*s3.HeadObjectOutput, error) {
		return nil, &smithy.GenericAPIError{Code: "NoSuchKey", Message: "https://private.example/bucket/key"}
	}}
	client := newClient(testConfig(), fake, nil, objectstorage.ObserverFunc(func(_ context.Context, value objectstorage.Observation) { observation = value }))
	_, err := client.Stat(context.Background(), objectstorage.ObjectRef{Key: "secret/key"})
	if !errors.Is(err, objectstorage.ErrNotFound) {
		t.Fatalf("Stat() error = %v", err)
	}
	if strings.Contains(err.Error(), "private.example") {
		t.Fatalf("错误文本泄露 provider 细节: %v", err)
	}
	if observation.Operation != "stat" || observation.Outcome != objectstorage.OutcomeError || observation.ErrorKind != objectstorage.ErrNotFound {
		t.Fatalf("observation = %+v", observation)
	}
}

func TestNewUsesInternalAndPresignEndpoints(t *testing.T) {
	t.Parallel()
	checked := make(chan string, 1)
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		checked <- request.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer internal.Close()
	public := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer public.Close()
	awsConfig := aws.Config{Region: "ignored", Credentials: aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider("key", "secret", "")), HTTPClient: internal.Client()}
	client, err := New(context.Background(), Config{
		Region: "test-1", Namespace: objectstorage.Namespace{Bucket: "bucket", Prefix: "prefix"},
		Endpoint: internal.URL, PresignEndpoint: public.URL, UsePathStyle: true,
	}, WithAWSConfig(awsConfig))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := client.Check(context.Background()); err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if path := <-checked; path != "/bucket" {
		t.Fatalf("内部 path style 路径 = %q", path)
	}
	presigned, err := client.PresignGet(context.Background(), objectstorage.PresignGetRequest{Object: objectstorage.ObjectRef{Key: "key"}})
	if err != nil {
		t.Fatalf("PresignGet() error = %v", err)
	}
	if !strings.HasPrefix(presigned.URL, public.URL+"/bucket/prefix/key?") {
		t.Fatalf("presigned URL = %q", presigned.URL)
	}
}

func TestNewRejectsInvalidAndDuplicateOptions(t *testing.T) {
	t.Parallel()
	base := Config{Region: "test-1", Namespace: objectstorage.Namespace{Bucket: "bucket"}}
	invalidConfigs := []Config{
		{},
		{Region: "test-1", Namespace: objectstorage.Namespace{Bucket: "bucket"}, PresignEndpoint: "https://public.example"},
		{Region: "test-1", Namespace: objectstorage.Namespace{Bucket: "bucket"}, Endpoint: "ftp://invalid"},
		{Region: "test-1", Namespace: objectstorage.Namespace{Bucket: "bucket"}, DefaultPresignTTL: time.Hour, MaxPresignTTL: 10 * time.Minute},
	}
	for _, config := range invalidConfigs {
		if _, err := New(context.Background(), config); !errors.Is(err, objectstorage.ErrInvalidArgument) {
			t.Fatalf("New(%+v) error = %v", config, err)
		}
	}
	awsConfig := aws.Config{Credentials: aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider("key", "secret", ""))}
	if _, err := New(context.Background(), base, WithAWSConfig(awsConfig), WithAWSConfig(awsConfig)); !errors.Is(err, objectstorage.ErrInvalidArgument) {
		t.Fatalf("duplicate option error = %v", err)
	}
	var nilOption Option
	if _, err := New(context.Background(), base, nilOption); !errors.Is(err, objectstorage.ErrInvalidArgument) {
		t.Fatalf("nil option error = %v", err)
	}
}

type trackingBody struct {
	io.Reader
	closed bool
}

func (b *trackingBody) Close() error { b.closed = true; return nil }
