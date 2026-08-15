// Package httpapi 暴露 Storage v1 控制面 HTTP API。
package httpapi

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strings"

	sharedhttp "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/http/api"
	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/objectstorage"
	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/storagecontract"
	"github.com/L1ndenbaum/stellarmesh-sdk/services/storage/internal/application"
)

// Readiness 报告全部 namespace 最近一次可访问性检查结果。
type Readiness interface {
	Ready() bool
}

// Handler 将严格 Storage v1 请求映射到 namespace store。
type Handler struct {
	registry  *application.Registry
	policy    *storagecontract.Policy
	readiness Readiness
}

// NewHandler 创建 storage-service HTTP handler。
func NewHandler(registry *application.Registry, policy *storagecontract.Policy, readiness Readiness) *Handler {
	return &Handler{registry: registry, policy: policy, readiness: readiness}
}

// HandleHealth 报告进程存活。
func (handler *Handler) HandleHealth(w http.ResponseWriter, _ *http.Request) {
	sharedhttp.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// HandleReady 报告全部 namespace 是否可访问。
func (handler *Handler) HandleReady(w http.ResponseWriter, _ *http.Request) {
	if handler.readiness == nil || !handler.readiness.Ready() {
		sharedhttp.WriteJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready"})
		return
	}
	sharedhttp.WriteJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

// HandleStat 读取对象元数据。
func (handler *Handler) HandleStat(w http.ResponseWriter, request *http.Request) {
	var payload storagecontract.ObjectRequest
	if !decode(w, request, &payload) || !handler.authorize(w, request, payload.Namespace, storagecontract.CapabilityRead) {
		return
	}
	store, namespace, ok := handler.storeAndValidate(w, payload.Namespace, payload.Key, payload.VersionID, "", nil)
	if !ok {
		return
	}
	_ = namespace
	info, err := store.Stat(request.Context(), objectstorage.ObjectRef{Key: payload.Key, VersionID: payload.VersionID})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	sharedhttp.WriteJSON(w, http.StatusOK, objectInfo(info))
}

// HandleDelete 删除当前对象语义或指定版本。
func (handler *Handler) HandleDelete(w http.ResponseWriter, request *http.Request) {
	var payload storagecontract.ObjectRequest
	if !decode(w, request, &payload) || !handler.authorize(w, request, payload.Namespace, storagecontract.CapabilityDelete) {
		return
	}
	store, _, ok := handler.storeAndValidate(w, payload.Namespace, payload.Key, payload.VersionID, "", nil)
	if !ok {
		return
	}
	if err := store.Delete(request.Context(), objectstorage.DeleteRequest{Object: objectstorage.ObjectRef{Key: payload.Key, VersionID: payload.VersionID}}); err != nil {
		writeStoreError(w, err)
		return
	}
	sharedhttp.WriteJSON(w, http.StatusOK, map[string]any{})
}

// HandlePresignGet 创建直接下载请求。
func (handler *Handler) HandlePresignGet(w http.ResponseWriter, request *http.Request) {
	var payload storagecontract.PresignGetRequest
	if !decode(w, request, &payload) || !handler.authorize(w, request, payload.Namespace, storagecontract.CapabilityRead) {
		return
	}
	store, _, ok := handler.storeAndValidate(w, payload.Namespace, payload.Key, payload.VersionID, "", nil)
	if !ok {
		return
	}
	ttl, err := storagecontract.PresignTTL(payload.ExpiresIn)
	if err != nil {
		sharedhttp.WriteError(w, http.StatusBadRequest, "invalid request")
		return
	}
	presigned, err := store.PresignGet(request.Context(), objectstorage.PresignGetRequest{
		Object: objectstorage.ObjectRef{Key: payload.Key, VersionID: payload.VersionID}, TTL: ttl,
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	sharedhttp.WriteJSON(w, http.StatusOK, presignedRequest(presigned))
}

// HandlePresignPut 创建直接上传请求。
func (handler *Handler) HandlePresignPut(w http.ResponseWriter, request *http.Request) {
	var payload storagecontract.PresignPutRequest
	if !decode(w, request, &payload) || !handler.authorize(w, request, payload.Namespace, storagecontract.CapabilityWrite) {
		return
	}
	store, _, ok := handler.storeAndValidate(w, payload.Namespace, payload.Key, "", payload.ContentType, payload.Metadata)
	if !ok || payload.Size < 0 || payload.Size > objectstorage.MaxSinglePutBytes {
		if ok {
			sharedhttp.WriteError(w, http.StatusBadRequest, "invalid request")
		}
		return
	}
	checksum, valid := parseChecksum(payload.Checksum)
	if !valid {
		sharedhttp.WriteError(w, http.StatusBadRequest, "invalid request")
		return
	}
	ttl, err := storagecontract.PresignTTL(payload.ExpiresIn)
	if err != nil {
		sharedhttp.WriteError(w, http.StatusBadRequest, "invalid request")
		return
	}
	presigned, err := store.PresignPut(request.Context(), objectstorage.PresignPutRequest{
		Key: payload.Key, Size: payload.Size, ContentType: payload.ContentType,
		Metadata: payload.Metadata, Checksum: checksum, TTL: ttl,
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	sharedhttp.WriteJSON(w, http.StatusOK, presignedRequest(presigned))
}

// HandleMultipartCreate 初始化 Multipart。
func (handler *Handler) HandleMultipartCreate(w http.ResponseWriter, request *http.Request) {
	var payload storagecontract.MultipartCreateRequest
	if !decode(w, request, &payload) || !handler.authorize(w, request, payload.Namespace, storagecontract.CapabilityWrite) {
		return
	}
	store, _, ok := handler.storeAndValidate(w, payload.Namespace, payload.Key, "", payload.ContentType, payload.Metadata)
	if !ok {
		return
	}
	checksum, valid := parseChecksum(payload.Checksum)
	if !valid {
		sharedhttp.WriteError(w, http.StatusBadRequest, "invalid request")
		return
	}
	upload, err := store.CreateMultipart(request.Context(), objectstorage.CreateMultipartRequest{
		Key: payload.Key, ContentType: payload.ContentType, Metadata: payload.Metadata, Checksum: checksum,
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	sharedhttp.WriteJSON(w, http.StatusOK, storagecontract.MultipartUpload{Key: upload.Key, UploadID: upload.UploadID})
}

// HandleMultipartPresignPart 为一个分片创建上传请求。
func (handler *Handler) HandleMultipartPresignPart(w http.ResponseWriter, request *http.Request) {
	var payload storagecontract.MultipartPartRequest
	if !decode(w, request, &payload) || !handler.authorize(w, request, payload.Namespace, storagecontract.CapabilityWrite) {
		return
	}
	store, _, ok := handler.storeAndValidate(w, payload.Namespace, payload.Key, "", "", nil)
	if !ok || payload.UploadID == "" || payload.PartNumber < storagecontract.MinPartNumber || payload.PartNumber > storagecontract.MaxPartNumber {
		if ok {
			sharedhttp.WriteError(w, http.StatusBadRequest, "invalid request")
		}
		return
	}
	ttl, err := storagecontract.PresignTTL(payload.ExpiresIn)
	if err != nil {
		sharedhttp.WriteError(w, http.StatusBadRequest, "invalid request")
		return
	}
	presigned, err := store.PresignPart(request.Context(), objectstorage.PresignPartRequest{
		Key: payload.Key, UploadID: payload.UploadID, PartNumber: payload.PartNumber, TTL: ttl,
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	sharedhttp.WriteJSON(w, http.StatusOK, presignedRequest(presigned))
}

// HandleMultipartComplete 完成显式 Multipart。
func (handler *Handler) HandleMultipartComplete(w http.ResponseWriter, request *http.Request) {
	var payload storagecontract.MultipartCompleteRequest
	if !decode(w, request, &payload) || !handler.authorize(w, request, payload.Namespace, storagecontract.CapabilityWrite) {
		return
	}
	store, _, ok := handler.storeAndValidate(w, payload.Namespace, payload.Key, "", "", nil)
	if !ok || payload.UploadID == "" || len(payload.Parts) == 0 {
		if ok {
			sharedhttp.WriteError(w, http.StatusBadRequest, "invalid request")
		}
		return
	}
	parts := make([]objectstorage.CompletedPart, len(payload.Parts))
	for index, part := range payload.Parts {
		parts[index] = objectstorage.CompletedPart{PartNumber: part.PartNumber, ETag: part.ETag}
	}
	info, err := store.CompleteMultipart(request.Context(), objectstorage.CompleteMultipartRequest{Key: payload.Key, UploadID: payload.UploadID, Parts: parts})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	sharedhttp.WriteJSON(w, http.StatusOK, objectInfo(info))
}

// HandleMultipartAbort 中止显式 Multipart。
func (handler *Handler) HandleMultipartAbort(w http.ResponseWriter, request *http.Request) {
	var payload storagecontract.MultipartAbortRequest
	if !decode(w, request, &payload) || !handler.authorize(w, request, payload.Namespace, storagecontract.CapabilityWrite) {
		return
	}
	store, _, ok := handler.storeAndValidate(w, payload.Namespace, payload.Key, "", "", nil)
	if !ok || payload.UploadID == "" {
		if ok {
			sharedhttp.WriteError(w, http.StatusBadRequest, "invalid request")
		}
		return
	}
	if err := store.AbortMultipart(request.Context(), objectstorage.AbortMultipartRequest{Key: payload.Key, UploadID: payload.UploadID}); err != nil {
		writeStoreError(w, err)
		return
	}
	sharedhttp.WriteJSON(w, http.StatusOK, map[string]any{})
}

func (handler *Handler) authorize(w http.ResponseWriter, request *http.Request, namespace string, capability storagecontract.Capability) bool {
	if err := storagecontract.ValidateNamespaceName(namespace); err != nil {
		sharedhttp.WriteError(w, http.StatusBadRequest, "invalid request")
		return false
	}
	switch handler.policy.Authorize(request.Header.Get(storagecontract.ServiceTokenHeader), namespace, capability) {
	case storagecontract.DecisionAllowed:
		return true
	case storagecontract.DecisionUnauthenticated:
		sharedhttp.WriteError(w, http.StatusUnauthorized, "invalid storage service token")
	default:
		sharedhttp.WriteError(w, http.StatusForbidden, "storage access forbidden")
	}
	return false
}

func (handler *Handler) storeAndValidate(w http.ResponseWriter, namespaceName, key, versionID, contentType string, metadata map[string]string) (application.Store, objectstorage.Namespace, bool) {
	store, exists := handler.registry.Store(namespaceName)
	namespace, configured := handler.policy.Namespace(namespaceName)
	if !exists || !configured {
		sharedhttp.WriteError(w, http.StatusForbidden, "storage access forbidden")
		return nil, objectstorage.Namespace{}, false
	}
	if err := storagecontract.ValidateObjectInput(namespace, key, versionID, contentType, metadata); err != nil {
		sharedhttp.WriteError(w, http.StatusBadRequest, "invalid request")
		return nil, objectstorage.Namespace{}, false
	}
	return store, namespace, true
}

func decode(w http.ResponseWriter, request *http.Request, target any) bool {
	err := sharedhttp.DecodeJSONWithOptions(w, request, target, sharedhttp.DecodeJSONOptions{
		MaxBytes: storagecontract.MaxControlBodyBytes, DisallowUnknownFields: true,
	})
	if err == nil {
		return true
	}
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		sharedhttp.WriteError(w, http.StatusRequestEntityTooLarge, "request body too large")
	} else {
		sharedhttp.WriteError(w, http.StatusBadRequest, "invalid request")
	}
	return false
}

func writeStoreError(w http.ResponseWriter, err error) {
	status := http.StatusServiceUnavailable
	message := "storage unavailable"
	switch {
	case errors.Is(err, objectstorage.ErrInvalidArgument):
		status, message = http.StatusBadRequest, "invalid request"
	case errors.Is(err, objectstorage.ErrNotFound):
		status, message = http.StatusNotFound, "object not found"
	case errors.Is(err, objectstorage.ErrConflict):
		status, message = http.StatusConflict, "storage conflict"
	case errors.Is(err, objectstorage.ErrPreconditionFailed):
		status, message = http.StatusPreconditionFailed, "storage precondition failed"
	}
	sharedhttp.WriteError(w, status, message)
}

func parseChecksum(checksum *storagecontract.Checksum) (objectstorage.HeaderChecksum, bool) {
	if checksum == nil {
		return objectstorage.HeaderChecksum{}, true
	}
	if checksum.Value == "" {
		return objectstorage.HeaderChecksum{}, false
	}
	if _, err := base64.StdEncoding.DecodeString(checksum.Value); err != nil {
		return objectstorage.HeaderChecksum{}, false
	}
	switch strings.ToUpper(checksum.Algorithm) {
	case "CRC32":
		return objectstorage.HeaderChecksum{CRC32: checksum.Value}, true
	case "CRC32C":
		return objectstorage.HeaderChecksum{CRC32C: checksum.Value}, true
	case "SHA1":
		return objectstorage.HeaderChecksum{SHA1: checksum.Value}, true
	case "SHA256":
		return objectstorage.HeaderChecksum{SHA256: checksum.Value}, true
	default:
		return objectstorage.HeaderChecksum{}, false
	}
}

func objectInfo(info objectstorage.ObjectInfo) storagecontract.ObjectInfo {
	result := storagecontract.ObjectInfo{
		Key: info.Key, VersionID: info.VersionID, ETag: info.ETag, Size: info.Size,
		ContentType: info.ContentType, Metadata: info.Metadata, Checksum: checksum(info.Checksum),
	}
	if !info.LastModified.IsZero() {
		lastModified := info.LastModified
		result.LastModified = &lastModified
	}
	if result.Metadata == nil {
		result.Metadata = map[string]string{}
	}
	return result
}

func checksum(value objectstorage.HeaderChecksum) *storagecontract.Checksum {
	switch {
	case value.CRC32 != "":
		return &storagecontract.Checksum{Algorithm: "CRC32", Value: value.CRC32}
	case value.CRC32C != "":
		return &storagecontract.Checksum{Algorithm: "CRC32C", Value: value.CRC32C}
	case value.SHA1 != "":
		return &storagecontract.Checksum{Algorithm: "SHA1", Value: value.SHA1}
	case value.SHA256 != "":
		return &storagecontract.Checksum{Algorithm: "SHA256", Value: value.SHA256}
	default:
		return nil
	}
}

func presignedRequest(request objectstorage.PresignedRequest) storagecontract.PresignedRequest {
	return storagecontract.PresignedRequest{
		Method: request.Method, URL: request.URL, Headers: request.Headers, ExpiresAt: request.ExpiresAt,
	}
}
