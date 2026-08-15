package s3store_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/objectstorage"
	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/objectstorage/s3store"
	"github.com/aws/aws-sdk-go-v2/credentials"
)

func TestMinIOIntegrationRange(t *testing.T) {
	if os.Getenv("STELLARMESH_STORAGE_MINIO_INTEGRATION") != "1" {
		t.Skip("仅由 MinIO 集成脚本启用")
	}
	endpoint := requiredEnvironment(t, "STELLARMESH_STORAGE_MINIO_ENDPOINT")
	bucket := requiredEnvironment(t, "STELLARMESH_STORAGE_MINIO_BUCKET")
	prefix := requiredEnvironment(t, "STELLARMESH_STORAGE_MINIO_PREFIX")
	region := requiredEnvironment(t, "AWS_REGION")
	accessKey := requiredEnvironment(t, "AWS_ACCESS_KEY_ID")
	secretKey := requiredEnvironment(t, "AWS_SECRET_ACCESS_KEY")
	client, err := s3store.New(context.Background(), s3store.Config{
		Region: region,
		Namespace: objectstorage.Namespace{
			Bucket: bucket,
			Prefix: prefix,
		},
		Endpoint:     endpoint,
		UsePathStyle: true,
	}, s3store.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	key := fmt.Sprintf("range-%d.bin", time.Now().UnixNano())
	payload := []byte("0123456789")
	info, err := client.Put(context.Background(), objectstorage.PutRequest{
		Key: key, Body: bytes.NewReader(payload), Size: int64(len(payload)),
	})
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	t.Cleanup(func() {
		_ = client.Delete(context.Background(), objectstorage.DeleteRequest{
			Object: objectstorage.ObjectRef{Key: key, VersionID: info.VersionID},
		})
	})
	end := int64(5)
	object, err := client.Get(context.Background(), objectstorage.GetRequest{
		Object: objectstorage.ObjectRef{Key: key},
		Range:  &objectstorage.ByteRange{Start: 2, End: &end},
	})
	if err != nil {
		t.Fatalf("Get(range) error = %v", err)
	}
	body, readErr := io.ReadAll(object.Body)
	closeErr := object.Body.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("读取或关闭 Range Body: %v, %v", readErr, closeErr)
	}
	if !bytes.Equal(body, []byte("2345")) {
		t.Fatalf("Range body = %q", body)
	}
}

func TestAWSManualIntegration(t *testing.T) {
	if os.Getenv("STELLARMESH_STORAGE_AWS_INTEGRATION") != "1" {
		t.Skip("仅由显式真实 AWS 手动验证启用")
	}
	region := requiredEnvironment(t, "AWS_REGION")
	bucket := requiredEnvironment(t, "STELLARMESH_STORAGE_AWS_BUCKET")
	prefix := requiredEnvironment(t, "STELLARMESH_STORAGE_AWS_PREFIX")
	client, err := s3store.New(context.Background(), s3store.Config{
		Region: region,
		Namespace: objectstorage.Namespace{
			Bucket: bucket,
			Prefix: prefix,
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := client.Check(ctx); err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	key := fmt.Sprintf("manual-%d.txt", time.Now().UnixNano())
	payload := []byte("stellarmesh aws manual integration")
	info, err := client.Put(ctx, objectstorage.PutRequest{
		Key: key, Body: bytes.NewReader(payload), Size: int64(len(payload)), ContentType: "text/plain",
	})
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		_ = client.Delete(cleanupCtx, objectstorage.DeleteRequest{
			Object: objectstorage.ObjectRef{Key: key, VersionID: info.VersionID},
		})
	})
	object, err := client.Get(ctx, objectstorage.GetRequest{Object: objectstorage.ObjectRef{Key: key, VersionID: info.VersionID}})
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	body, readErr := io.ReadAll(object.Body)
	closeErr := object.Body.Close()
	if readErr != nil || closeErr != nil || !bytes.Equal(body, payload) {
		t.Fatalf("AWS 内容验证失败: read=%v close=%v", readErr, closeErr)
	}
}

func requiredEnvironment(t *testing.T, key string) string {
	t.Helper()
	value := os.Getenv(key)
	if value == "" {
		t.Fatalf("缺少环境变量 %s", key)
	}
	return value
}
