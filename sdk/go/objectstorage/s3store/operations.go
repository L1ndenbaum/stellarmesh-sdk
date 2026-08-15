package s3store

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/objectstorage"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// Check 只检查绑定 Bucket 的可访问性，绝不创建 Bucket。
func (c *Client) Check(ctx context.Context) (err error) {
	started := c.now()
	defer func() { c.observe(ctx, started, "check", 0, err) }()
	_, providerErr := c.api.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(c.config.Namespace.Bucket)})
	if providerErr != nil {
		return wrapProviderError("check", "", providerErr)
	}
	return nil
}

// Stat 读取对象元数据，不读取对象内容。
func (c *Client) Stat(ctx context.Context, ref objectstorage.ObjectRef) (info objectstorage.ObjectInfo, err error) {
	started := c.now()
	defer func() { c.observe(ctx, started, "stat", 0, err) }()
	if err := c.validateRef("stat", ref); err != nil {
		return objectstorage.ObjectInfo{}, err
	}
	output, providerErr := c.api.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket:       aws.String(c.config.Namespace.Bucket),
		Key:          aws.String(c.physicalKey(ref.Key)),
		VersionId:    stringPointer(ref.VersionID),
		ChecksumMode: types.ChecksumModeEnabled,
	})
	if providerErr != nil {
		return objectstorage.ObjectInfo{}, wrapProviderError("stat", ref.Key, providerErr)
	}
	return infoFromHead(ref.Key, output), nil
}

// Get 流式读取完整对象或指定字节范围；调用方必须关闭返回的 Body。
func (c *Client) Get(ctx context.Context, request objectstorage.GetRequest) (object *objectstorage.Object, err error) {
	started := c.now()
	defer func() {
		var bytes int64
		if object != nil {
			bytes = object.Size
		}
		c.observe(ctx, started, "get", bytes, err)
	}()
	if err := c.validateRef("get", request.Object); err != nil {
		return nil, err
	}
	if err := validateRange("get", request.Object.Key, request.Range); err != nil {
		return nil, err
	}
	output, providerErr := c.api.GetObject(ctx, &s3.GetObjectInput{
		Bucket:       aws.String(c.config.Namespace.Bucket),
		Key:          aws.String(c.physicalKey(request.Object.Key)),
		VersionId:    stringPointer(request.Object.VersionID),
		Range:        formatRange(request.Range),
		ChecksumMode: types.ChecksumModeEnabled,
	})
	if providerErr != nil {
		return nil, wrapProviderError("get", request.Object.Key, providerErr)
	}
	return &objectstorage.Object{ObjectInfo: infoFromGet(request.Object.Key, output), Body: output.Body}, nil
}

// Put 以已知长度流式上传对象，不创建临时文件。
func (c *Client) Put(ctx context.Context, request objectstorage.PutRequest) (info objectstorage.ObjectInfo, err error) {
	started := c.now()
	defer func() { c.observe(ctx, started, "put", request.Size, err) }()
	if err := validatePutRequest("put", c.config.Namespace, request); err != nil {
		return objectstorage.ObjectInfo{}, err
	}
	input := &s3.PutObjectInput{
		Bucket:        aws.String(c.config.Namespace.Bucket),
		Key:           aws.String(c.physicalKey(request.Key)),
		Body:          request.Body,
		ContentLength: aws.Int64(request.Size),
		ContentType:   stringPointer(request.ContentType),
		Metadata:      cloneMetadata(request.Metadata),
		IfMatch:       stringPointer(request.IfMatch),
		IfNoneMatch:   stringPointer(request.IfNoneMatch),
	}
	applyPutChecksums(input, request.Checksum)
	output, providerErr := c.api.PutObject(ctx, input)
	if providerErr != nil {
		return objectstorage.ObjectInfo{}, wrapProviderError("put", request.Key, providerErr)
	}
	return objectstorage.ObjectInfo{
		Key:         request.Key,
		VersionID:   aws.ToString(output.VersionId),
		ETag:        aws.ToString(output.ETag),
		Size:        request.Size,
		ContentType: request.ContentType,
		Metadata:    cloneMetadata(request.Metadata),
		Checksum: objectstorage.HeaderChecksum{
			CRC32:  aws.ToString(output.ChecksumCRC32),
			CRC32C: aws.ToString(output.ChecksumCRC32C),
			SHA1:   aws.ToString(output.ChecksumSHA1),
			SHA256: aws.ToString(output.ChecksumSHA256),
		},
	}, nil
}

// Delete 删除当前对象语义或指定版本。
func (c *Client) Delete(ctx context.Context, request objectstorage.DeleteRequest) (err error) {
	started := c.now()
	defer func() { c.observe(ctx, started, "delete", 0, err) }()
	if err := c.validateRef("delete", request.Object); err != nil {
		return err
	}
	_, providerErr := c.api.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket:    aws.String(c.config.Namespace.Bucket),
		Key:       aws.String(c.physicalKey(request.Object.Key)),
		VersionId: stringPointer(request.Object.VersionID),
		IfMatch:   stringPointer(request.IfMatch),
	})
	if providerErr != nil {
		return wrapProviderError("delete", request.Object.Key, providerErr)
	}
	return nil
}

// PresignGet 创建客户端可直接执行的数据面下载请求。
func (c *Client) PresignGet(ctx context.Context, request objectstorage.PresignGetRequest) (presigned objectstorage.PresignedRequest, err error) {
	started := c.now()
	defer func() { c.observe(ctx, started, "presign_get", 0, err) }()
	if err := c.validateRef("presign_get", request.Object); err != nil {
		return objectstorage.PresignedRequest{}, err
	}
	if err := validateRange("presign_get", request.Object.Key, request.Range); err != nil {
		return objectstorage.PresignedRequest{}, err
	}
	ttl, err := c.ttl(request.TTL, "presign_get", request.Object.Key)
	if err != nil {
		return objectstorage.PresignedRequest{}, err
	}
	output, providerErr := c.presigner.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket:    aws.String(c.config.Namespace.Bucket),
		Key:       aws.String(c.physicalKey(request.Object.Key)),
		VersionId: stringPointer(request.Object.VersionID),
		Range:     formatRange(request.Range),
	}, func(options *s3.PresignOptions) { options.Expires = ttl })
	if providerErr != nil {
		return objectstorage.PresignedRequest{}, wrapProviderError("presign_get", request.Object.Key, providerErr)
	}
	return presignedResult(output.Method, output.URL, output.SignedHeader, c.now().Add(ttl)), nil
}

// PresignPut 创建客户端可直接执行的数据面上传请求。
func (c *Client) PresignPut(ctx context.Context, request objectstorage.PresignPutRequest) (presigned objectstorage.PresignedRequest, err error) {
	started := c.now()
	defer func() { c.observe(ctx, started, "presign_put", request.Size, err) }()
	if err := validatePresignPutRequest(c.config.Namespace, request); err != nil {
		return objectstorage.PresignedRequest{}, err
	}
	ttl, err := c.ttl(request.TTL, "presign_put", request.Key)
	if err != nil {
		return objectstorage.PresignedRequest{}, err
	}
	input := &s3.PutObjectInput{
		Bucket:        aws.String(c.config.Namespace.Bucket),
		Key:           aws.String(c.physicalKey(request.Key)),
		ContentLength: aws.Int64(request.Size),
		ContentType:   stringPointer(request.ContentType),
		Metadata:      cloneMetadata(request.Metadata),
		IfMatch:       stringPointer(request.IfMatch),
		IfNoneMatch:   stringPointer(request.IfNoneMatch),
	}
	applyPutChecksums(input, request.Checksum)
	output, providerErr := c.presigner.PresignPutObject(ctx, input, func(options *s3.PresignOptions) { options.Expires = ttl })
	if providerErr != nil {
		return objectstorage.PresignedRequest{}, wrapProviderError("presign_put", request.Key, providerErr)
	}
	return presignedResult(output.Method, output.URL, output.SignedHeader, c.now().Add(ttl)), nil
}

// CreateMultipart 初始化显式 Multipart 上传。
func (c *Client) CreateMultipart(ctx context.Context, request objectstorage.CreateMultipartRequest) (upload objectstorage.MultipartUpload, err error) {
	started := c.now()
	defer func() { c.observe(ctx, started, "multipart_create", 0, err) }()
	if err := objectstorage.ValidateKey(c.config.Namespace, request.Key); err != nil {
		return objectstorage.MultipartUpload{}, wrapInvalid("multipart_create", request.Key, err)
	}
	if err := validateUploadFields("multipart_create", request.Key, request.ContentType, request.Metadata, request.Checksum); err != nil {
		return objectstorage.MultipartUpload{}, err
	}
	input := &s3.CreateMultipartUploadInput{
		Bucket:      aws.String(c.config.Namespace.Bucket),
		Key:         aws.String(c.physicalKey(request.Key)),
		ContentType: stringPointer(request.ContentType),
		Metadata:    cloneMetadata(request.Metadata),
	}
	input.ChecksumAlgorithm = checksumAlgorithm(request.Checksum)
	output, providerErr := c.api.CreateMultipartUpload(ctx, input)
	if providerErr != nil {
		return objectstorage.MultipartUpload{}, wrapProviderError("multipart_create", request.Key, providerErr)
	}
	if aws.ToString(output.UploadId) == "" {
		return objectstorage.MultipartUpload{}, wrapProviderError("multipart_create", request.Key, fmt.Errorf("provider 未返回 upload ID"))
	}
	return objectstorage.MultipartUpload{Key: request.Key, UploadID: aws.ToString(output.UploadId)}, nil
}

// PresignPart 为单个 Multipart 分片创建上传请求。
func (c *Client) PresignPart(ctx context.Context, request objectstorage.PresignPartRequest) (presigned objectstorage.PresignedRequest, err error) {
	started := c.now()
	defer func() { c.observe(ctx, started, "multipart_presign_part", 0, err) }()
	if err := objectstorage.ValidateKey(c.config.Namespace, request.Key); err != nil {
		return objectstorage.PresignedRequest{}, wrapInvalid("multipart_presign_part", request.Key, err)
	}
	if err := validateUploadID("multipart_presign_part", request.Key, request.UploadID); err != nil {
		return objectstorage.PresignedRequest{}, err
	}
	if err := validatePartNumber("multipart_presign_part", request.Key, request.PartNumber); err != nil {
		return objectstorage.PresignedRequest{}, err
	}
	ttl, err := c.ttl(request.TTL, "multipart_presign_part", request.Key)
	if err != nil {
		return objectstorage.PresignedRequest{}, err
	}
	output, providerErr := c.presigner.PresignUploadPart(ctx, &s3.UploadPartInput{
		Bucket:     aws.String(c.config.Namespace.Bucket),
		Key:        aws.String(c.physicalKey(request.Key)),
		UploadId:   aws.String(request.UploadID),
		PartNumber: aws.Int32(request.PartNumber),
	}, func(options *s3.PresignOptions) { options.Expires = ttl })
	if providerErr != nil {
		return objectstorage.PresignedRequest{}, wrapProviderError("multipart_presign_part", request.Key, providerErr)
	}
	return presignedResult(output.Method, output.URL, output.SignedHeader, c.now().Add(ttl)), nil
}

// CompleteMultipart 验证、复制并排序分片后完成上传。
func (c *Client) CompleteMultipart(ctx context.Context, request objectstorage.CompleteMultipartRequest) (info objectstorage.ObjectInfo, err error) {
	started := c.now()
	defer func() { c.observe(ctx, started, "multipart_complete", 0, err) }()
	if err := objectstorage.ValidateKey(c.config.Namespace, request.Key); err != nil {
		return objectstorage.ObjectInfo{}, wrapInvalid("multipart_complete", request.Key, err)
	}
	if err := validateUploadID("multipart_complete", request.Key, request.UploadID); err != nil {
		return objectstorage.ObjectInfo{}, err
	}
	parts, err := validateAndSortParts("multipart_complete", request.Key, request.Parts)
	if err != nil {
		return objectstorage.ObjectInfo{}, err
	}
	output, providerErr := c.api.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:   aws.String(c.config.Namespace.Bucket),
		Key:      aws.String(c.physicalKey(request.Key)),
		UploadId: aws.String(request.UploadID),
		MultipartUpload: &types.CompletedMultipartUpload{
			Parts: completedParts(parts),
		},
		IfMatch:     stringPointer(request.IfMatch),
		IfNoneMatch: stringPointer(request.IfNoneMatch),
	})
	if providerErr != nil {
		return objectstorage.ObjectInfo{}, wrapProviderError("multipart_complete", request.Key, providerErr)
	}
	return objectstorage.ObjectInfo{
		Key:       request.Key,
		VersionID: aws.ToString(output.VersionId),
		ETag:      aws.ToString(output.ETag),
		Checksum: objectstorage.HeaderChecksum{
			CRC32:  aws.ToString(output.ChecksumCRC32),
			CRC32C: aws.ToString(output.ChecksumCRC32C),
			SHA1:   aws.ToString(output.ChecksumSHA1),
			SHA256: aws.ToString(output.ChecksumSHA256),
		},
	}, nil
}

// AbortMultipart 中止显式 Multipart 上传。
func (c *Client) AbortMultipart(ctx context.Context, request objectstorage.AbortMultipartRequest) (err error) {
	started := c.now()
	defer func() { c.observe(ctx, started, "multipart_abort", 0, err) }()
	if err := objectstorage.ValidateKey(c.config.Namespace, request.Key); err != nil {
		return wrapInvalid("multipart_abort", request.Key, err)
	}
	if err := validateUploadID("multipart_abort", request.Key, request.UploadID); err != nil {
		return err
	}
	_, providerErr := c.api.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
		Bucket:   aws.String(c.config.Namespace.Bucket),
		Key:      aws.String(c.physicalKey(request.Key)),
		UploadId: aws.String(request.UploadID),
	})
	if providerErr != nil {
		return wrapProviderError("multipart_abort", request.Key, providerErr)
	}
	return nil
}

func infoFromHead(key string, output *s3.HeadObjectOutput) objectstorage.ObjectInfo {
	return objectstorage.ObjectInfo{
		Key:          key,
		VersionID:    aws.ToString(output.VersionId),
		ETag:         aws.ToString(output.ETag),
		Size:         aws.ToInt64(output.ContentLength),
		ContentType:  aws.ToString(output.ContentType),
		LastModified: aws.ToTime(output.LastModified),
		Metadata:     cloneMetadata(output.Metadata),
		Checksum:     checksums(output.ChecksumCRC32, output.ChecksumCRC32C, output.ChecksumSHA1, output.ChecksumSHA256),
	}
}

func infoFromGet(key string, output *s3.GetObjectOutput) objectstorage.ObjectInfo {
	return objectstorage.ObjectInfo{
		Key:          key,
		VersionID:    aws.ToString(output.VersionId),
		ETag:         aws.ToString(output.ETag),
		Size:         aws.ToInt64(output.ContentLength),
		ContentType:  aws.ToString(output.ContentType),
		LastModified: aws.ToTime(output.LastModified),
		Metadata:     cloneMetadata(output.Metadata),
		Checksum:     checksums(output.ChecksumCRC32, output.ChecksumCRC32C, output.ChecksumSHA1, output.ChecksumSHA256),
	}
}

func checksums(crc32, crc32c, sha1, sha256 *string) objectstorage.HeaderChecksum {
	return objectstorage.HeaderChecksum{
		CRC32: aws.ToString(crc32), CRC32C: aws.ToString(crc32c),
		SHA1: aws.ToString(sha1), SHA256: aws.ToString(sha256),
	}
}

func applyPutChecksums(input *s3.PutObjectInput, checksum objectstorage.HeaderChecksum) {
	input.ChecksumAlgorithm = checksumAlgorithm(checksum)
	input.ChecksumCRC32 = stringPointer(checksum.CRC32)
	input.ChecksumCRC32C = stringPointer(checksum.CRC32C)
	input.ChecksumSHA1 = stringPointer(checksum.SHA1)
	input.ChecksumSHA256 = stringPointer(checksum.SHA256)
}

func checksumAlgorithm(checksum objectstorage.HeaderChecksum) types.ChecksumAlgorithm {
	switch {
	case checksum.CRC32 != "":
		return types.ChecksumAlgorithmCrc32
	case checksum.CRC32C != "":
		return types.ChecksumAlgorithmCrc32c
	case checksum.SHA1 != "":
		return types.ChecksumAlgorithmSha1
	case checksum.SHA256 != "":
		return types.ChecksumAlgorithmSha256
	default:
		return ""
	}
}

func presignedResult(method, url string, headers map[string][]string, expiresAt time.Time) objectstorage.PresignedRequest {
	return objectstorage.PresignedRequest{Method: method, URL: url, Headers: copyHeaders(headers), ExpiresAt: expiresAt}
}

func cloneMetadata(metadata map[string]string) map[string]string {
	if metadata == nil {
		return nil
	}
	result := make(map[string]string, len(metadata))
	keys := make([]string, 0, len(metadata))
	for key := range metadata {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		result[key] = metadata[key]
	}
	return result
}
