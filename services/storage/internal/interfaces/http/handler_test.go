package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/objectstorage"
	"github.com/L1ndenbaum/stellarmesh-sdk/services/storage/internal/application"
	httpapi "github.com/L1ndenbaum/stellarmesh-sdk/services/storage/internal/interfaces/http"
	"github.com/L1ndenbaum/stellarmesh-sdk/services/storage/internal/storagev1"
)

const validToken = "storage-service-test-token-0000000001"

func TestRouterHealthAuthenticationAndReadiness(t *testing.T) {
	t.Parallel()
	ready := &readiness{value: false}
	router := testRouter(t, &fakeStore{}, ready, []storagev1.Capability{
		storagev1.CapabilityRead, storagev1.CapabilityWrite, storagev1.CapabilityDelete,
	})
	if response := perform(router, http.MethodGet, "/health", "", ""); response.Code != http.StatusOK {
		t.Fatalf("health status = %d", response.Code)
	}
	if response := perform(router, http.MethodPost, "/v1/objects/stat", `{"namespace":"documents","key":"key"}`, "wrong"); response.Code != http.StatusUnauthorized {
		t.Fatalf("invalid token status = %d", response.Code)
	}
	if response := perform(router, http.MethodPost, "/v1/objects/stat", `{"namespace":"documents","key":"key"}`, validToken); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("not ready status = %d", response.Code)
	}
	ready.value = true
	if response := perform(router, http.MethodGet, "/health/ready", "", ""); response.Code != http.StatusOK {
		t.Fatalf("ready status = %d", response.Code)
	}
}

func TestRoutesRejectUnknownFieldsAndEnforceCapability(t *testing.T) {
	t.Parallel()
	router := testRouter(t, &fakeStore{}, &readiness{value: true}, []storagev1.Capability{storagev1.CapabilityRead})
	response := perform(router, http.MethodPost, "/v1/objects/stat", `{"namespace":"documents","key":"key","bucket":"forbidden"}`, validToken)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unknown field status = %d body=%s", response.Code, response.Body.String())
	}
	response = perform(router, http.MethodPost, "/v1/objects/delete", `{"namespace":"documents","key":"key"}`, validToken)
	if response.Code != http.StatusForbidden {
		t.Fatalf("capability status = %d", response.Code)
	}
	large := `{"namespace":"documents","key":"` + strings.Repeat("x", storagev1.MaxControlBodyBytes) + `"}`
	response = perform(router, http.MethodPost, "/v1/objects/stat", large, validToken)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("large body status = %d", response.Code)
	}
}

func TestAllControlRoutes(t *testing.T) {
	t.Parallel()
	router := testRouter(t, &fakeStore{}, &readiness{value: true}, []storagev1.Capability{
		storagev1.CapabilityRead, storagev1.CapabilityWrite, storagev1.CapabilityDelete,
	})
	tests := []struct {
		route   string
		payload string
	}{
		{route: "/v1/objects/stat", payload: `{"namespace":"documents","key":"key"}`},
		{route: "/v1/objects/delete", payload: `{"namespace":"documents","key":"key"}`},
		{route: "/v1/presign/get", payload: `{"namespace":"documents","key":"key","expires_in":60}`},
		{route: "/v1/presign/put", payload: `{"namespace":"documents","key":"key","size":7,"content_type":"text/plain","metadata":{"source":"test"},"expires_in":60}`},
		{route: "/v1/multipart/create", payload: `{"namespace":"documents","key":"key"}`},
		{route: "/v1/multipart/presign-part", payload: `{"namespace":"documents","key":"key","upload_id":"upload","part_number":1}`},
		{route: "/v1/multipart/complete", payload: `{"namespace":"documents","key":"key","upload_id":"upload","parts":[{"part_number":1,"etag":"opaque"}]}`},
		{route: "/v1/multipart/abort", payload: `{"namespace":"documents","key":"key","upload_id":"upload"}`},
	}
	for _, test := range tests {
		test := test
		t.Run(test.route, func(t *testing.T) {
			t.Parallel()
			response := perform(router, http.MethodPost, test.route, test.payload, validToken)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
			}
			var envelope struct {
				Data json.RawMessage `json:"data"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil || len(envelope.Data) == 0 {
				t.Fatalf("响应 envelope 无效: %v %s", err, response.Body.String())
			}
		})
	}
}

func TestProviderErrorsAreMappedWithoutDetails(t *testing.T) {
	t.Parallel()
	tests := []struct {
		kind   error
		status int
	}{
		{kind: objectstorage.ErrNotFound, status: http.StatusNotFound},
		{kind: objectstorage.ErrConflict, status: http.StatusConflict},
		{kind: objectstorage.ErrPreconditionFailed, status: http.StatusPreconditionFailed},
		{kind: objectstorage.ErrForbidden, status: http.StatusServiceUnavailable},
		{kind: objectstorage.ErrUnavailable, status: http.StatusServiceUnavailable},
	}
	for _, test := range tests {
		store := &fakeStore{statErr: &objectstorage.Error{
			Kind: test.kind, Operation: "stat", Key: "key", Err: errors.New("https://internal.example/private-bucket?X-Amz-Signature=secret"),
		}}
		router := testRouter(t, store, &readiness{value: true}, []storagev1.Capability{storagev1.CapabilityRead})
		response := perform(router, http.MethodPost, "/v1/objects/stat", `{"namespace":"documents","key":"key"}`, validToken)
		if response.Code != test.status {
			t.Fatalf("kind %v status = %d", test.kind, response.Code)
		}
		body := response.Body.String()
		if strings.Contains(body, "internal.example") || strings.Contains(body, "private-bucket") || strings.Contains(body, "Signature") {
			t.Fatalf("响应泄露 provider 信息: %s", body)
		}
	}
}

func testRouter(t *testing.T, store application.Store, ready httpapi.Readiness, capabilities []storagev1.Capability) http.Handler {
	t.Helper()
	policy, err := storagev1.CompilePolicy(storagev1.AccessConfig{
		Namespaces: map[string]storagev1.NamespaceConfig{"documents": {Bucket: "bucket", Prefix: "prefix/"}},
		Principals: map[string]storagev1.PrincipalConfig{"backend": {
			Tokens: []string{validToken}, Grants: map[string][]storagev1.Capability{"documents": capabilities},
		}},
	})
	if err != nil {
		t.Fatalf("CompilePolicy() error = %v", err)
	}
	registry := application.NewRegistry(map[string]application.Store{"documents": store})
	return httpapi.NewRouter(httpapi.NewHandler(registry, policy, ready), nil)
}

func perform(handler http.Handler, method, path, body, token string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		request.Header.Set(storagev1.ServiceTokenHeader, token)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

type readiness struct{ value bool }

func (readiness *readiness) Ready() bool { return readiness.value }

type fakeStore struct{ statErr error }

func (*fakeStore) Check(context.Context) error { return nil }
func (store *fakeStore) Stat(context.Context, objectstorage.ObjectRef) (objectstorage.ObjectInfo, error) {
	if store.statErr != nil {
		return objectstorage.ObjectInfo{}, store.statErr
	}
	return objectstorage.ObjectInfo{Key: "key", ETag: "opaque", Size: 7, Metadata: map[string]string{}}, nil
}
func (*fakeStore) Get(context.Context, objectstorage.GetRequest) (*objectstorage.Object, error) {
	return &objectstorage.Object{Body: io.NopCloser(bytes.NewReader(nil))}, nil
}
func (*fakeStore) Put(context.Context, objectstorage.PutRequest) (objectstorage.ObjectInfo, error) {
	return objectstorage.ObjectInfo{}, nil
}
func (*fakeStore) Delete(context.Context, objectstorage.DeleteRequest) error { return nil }
func (*fakeStore) PresignGet(context.Context, objectstorage.PresignGetRequest) (objectstorage.PresignedRequest, error) {
	return objectstorage.PresignedRequest{Method: http.MethodGet, URL: "https://object.example/key?signature=value", Headers: map[string][]string{}, ExpiresAt: time.Now().Add(time.Minute)}, nil
}
func (*fakeStore) PresignPut(context.Context, objectstorage.PresignPutRequest) (objectstorage.PresignedRequest, error) {
	return objectstorage.PresignedRequest{Method: http.MethodPut, URL: "https://object.example/key?signature=value", Headers: map[string][]string{"Content-Type": {"text/plain"}}, ExpiresAt: time.Now().Add(time.Minute)}, nil
}
func (*fakeStore) CreateMultipart(context.Context, objectstorage.CreateMultipartRequest) (objectstorage.MultipartUpload, error) {
	return objectstorage.MultipartUpload{Key: "key", UploadID: "upload"}, nil
}
func (*fakeStore) PresignPart(context.Context, objectstorage.PresignPartRequest) (objectstorage.PresignedRequest, error) {
	return objectstorage.PresignedRequest{Method: http.MethodPut, URL: "https://object.example/part?signature=value", Headers: map[string][]string{}, ExpiresAt: time.Now().Add(time.Minute)}, nil
}
func (*fakeStore) CompleteMultipart(context.Context, objectstorage.CompleteMultipartRequest) (objectstorage.ObjectInfo, error) {
	return objectstorage.ObjectInfo{Key: "key", ETag: "opaque", Metadata: map[string]string{}}, nil
}
func (*fakeStore) AbortMultipart(context.Context, objectstorage.AbortMultipartRequest) error {
	return nil
}
