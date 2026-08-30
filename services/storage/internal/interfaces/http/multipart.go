package httpapi

import (
	"net/http"

	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/objectstorage"
	"github.com/L1ndenbaum/stellarmesh-sdk/services/storage/internal/storagev1"
)

// HandleMultipartCreate 初始化 Multipart。
func (handler *Handler) HandleMultipartCreate(w http.ResponseWriter, request *http.Request) {
	var payload storagev1.MultipartCreateRequest
	if !decode(w, request, &payload) || !handler.authorize(w, request, payload.Namespace, storagev1.CapabilityWrite) {
		return
	}
	store, _, ok := handler.storeAndValidate(w, payload.Namespace, payload.Key, "", payload.ContentType, payload.Metadata)
	if !ok {
		return
	}
	checksum, valid := parseChecksum(payload.Checksum)
	if !valid {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	upload, err := store.CreateMultipart(request.Context(), objectstorage.CreateMultipartRequest{
		Key: payload.Key, ContentType: payload.ContentType, Metadata: payload.Metadata, Checksum: checksum,
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, storagev1.MultipartUpload{Key: upload.Key, UploadID: upload.UploadID})
}

// HandleMultipartPresignPart 为一个分片创建上传请求。
func (handler *Handler) HandleMultipartPresignPart(w http.ResponseWriter, request *http.Request) {
	var payload storagev1.MultipartPartRequest
	if !decode(w, request, &payload) || !handler.authorize(w, request, payload.Namespace, storagev1.CapabilityWrite) {
		return
	}
	store, _, ok := handler.storeAndValidate(w, payload.Namespace, payload.Key, "", "", nil)
	if !ok || payload.UploadID == "" || payload.PartNumber < storagev1.MinPartNumber || payload.PartNumber > storagev1.MaxPartNumber {
		if ok {
			writeError(w, http.StatusBadRequest, "invalid request")
		}
		return
	}
	ttl, err := storagev1.PresignTTL(payload.ExpiresIn)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	presigned, err := store.PresignPart(request.Context(), objectstorage.PresignPartRequest{
		Key: payload.Key, UploadID: payload.UploadID, PartNumber: payload.PartNumber, TTL: ttl,
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, presignedRequest(presigned))
}

// HandleMultipartComplete 完成显式 Multipart。
func (handler *Handler) HandleMultipartComplete(w http.ResponseWriter, request *http.Request) {
	var payload storagev1.MultipartCompleteRequest
	if !decode(w, request, &payload) || !handler.authorize(w, request, payload.Namespace, storagev1.CapabilityWrite) {
		return
	}
	store, _, ok := handler.storeAndValidate(w, payload.Namespace, payload.Key, "", "", nil)
	if !ok || payload.UploadID == "" || len(payload.Parts) == 0 {
		if ok {
			writeError(w, http.StatusBadRequest, "invalid request")
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
	writeJSON(w, http.StatusOK, objectInfo(info))
}

// HandleMultipartAbort 中止显式 Multipart。
func (handler *Handler) HandleMultipartAbort(w http.ResponseWriter, request *http.Request) {
	var payload storagev1.MultipartAbortRequest
	if !decode(w, request, &payload) || !handler.authorize(w, request, payload.Namespace, storagev1.CapabilityWrite) {
		return
	}
	store, _, ok := handler.storeAndValidate(w, payload.Namespace, payload.Key, "", "", nil)
	if !ok || payload.UploadID == "" {
		if ok {
			writeError(w, http.StatusBadRequest, "invalid request")
		}
		return
	}
	if err := store.AbortMultipart(request.Context(), objectstorage.AbortMultipartRequest{Key: payload.Key, UploadID: payload.UploadID}); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}
