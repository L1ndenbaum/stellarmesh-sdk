package application_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/objectstorage"
	"github.com/L1ndenbaum/stellarmesh-sdk/services/storage/internal/application"
)

func TestHealthFailsClosedAndRecovers(t *testing.T) {
	t.Parallel()
	store := &healthStore{err: errors.New("unavailable")}
	health := application.NewHealth(application.NewRegistry(map[string]application.Store{"documents": store}))
	if health.Ready() {
		t.Fatal("初始 readiness 必须为 false")
	}
	if health.CheckAll(context.Background(), time.Second) || health.Ready() {
		t.Fatal("检查失败时 readiness 必须为 false")
	}
	store.setError(nil)
	if !health.CheckAll(context.Background(), time.Second) || !health.Ready() {
		t.Fatal("依赖恢复后 readiness 应恢复")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); health.Run(ctx, time.Second, time.Hour) }()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run() 未响应取消")
	}
	if health.Ready() {
		t.Fatal("停止健康循环后 readiness 必须为 false")
	}
}

type healthStore struct {
	mu  sync.RWMutex
	err error
}

func (store *healthStore) setError(err error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.err = err
}
func (store *healthStore) Check(context.Context) error {
	store.mu.RLock()
	defer store.mu.RUnlock()
	return store.err
}
func (*healthStore) Stat(context.Context, objectstorage.ObjectRef) (objectstorage.ObjectInfo, error) {
	return objectstorage.ObjectInfo{}, nil
}
func (*healthStore) Get(context.Context, objectstorage.GetRequest) (*objectstorage.Object, error) {
	return nil, nil
}
func (*healthStore) Put(context.Context, objectstorage.PutRequest) (objectstorage.ObjectInfo, error) {
	return objectstorage.ObjectInfo{}, nil
}
func (*healthStore) Delete(context.Context, objectstorage.DeleteRequest) error { return nil }
func (*healthStore) PresignGet(context.Context, objectstorage.PresignGetRequest) (objectstorage.PresignedRequest, error) {
	return objectstorage.PresignedRequest{}, nil
}
func (*healthStore) PresignPut(context.Context, objectstorage.PresignPutRequest) (objectstorage.PresignedRequest, error) {
	return objectstorage.PresignedRequest{}, nil
}
func (*healthStore) CreateMultipart(context.Context, objectstorage.CreateMultipartRequest) (objectstorage.MultipartUpload, error) {
	return objectstorage.MultipartUpload{}, nil
}
func (*healthStore) PresignPart(context.Context, objectstorage.PresignPartRequest) (objectstorage.PresignedRequest, error) {
	return objectstorage.PresignedRequest{}, nil
}
func (*healthStore) CompleteMultipart(context.Context, objectstorage.CompleteMultipartRequest) (objectstorage.ObjectInfo, error) {
	return objectstorage.ObjectInfo{}, nil
}
func (*healthStore) AbortMultipart(context.Context, objectstorage.AbortMultipartRequest) error {
	return nil
}
