package httpapi

import (
	"net/http"

	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/objectstorage"
	"github.com/L1ndenbaum/stellarmesh-sdk/services/storage/internal/storagev1"
)

// HandlePresignGet 创建直接下载请求。
func (handler *Handler) HandlePresignGet(w http.ResponseWriter, request *http.Request) {
	var payload storagev1.PresignGetRequest
	if !decode(w, request, &payload) || !handler.authorize(w, request, payload.Namespace, storagev1.CapabilityRead) {
		return
	}
	store, _, ok := handler.storeAndValidate(w, payload.Namespace, payload.Key, payload.VersionID, "", nil)
	if !ok {
		return
	}
	ttl, err := storagev1.PresignTTL(payload.ExpiresIn)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	presigned, err := store.PresignGet(request.Context(), objectstorage.PresignGetRequest{
		Object: objectstorage.ObjectRef{Key: payload.Key, VersionID: payload.VersionID}, TTL: ttl,
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, presignedRequest(presigned))
}

// HandlePresignPut 创建直接上传请求。
func (handler *Handler) HandlePresignPut(w http.ResponseWriter, request *http.Request) {
	var payload storagev1.PresignPutRequest
	if !decode(w, request, &payload) || !handler.authorize(w, request, payload.Namespace, storagev1.CapabilityWrite) {
		return
	}
	store, _, ok := handler.storeAndValidate(w, payload.Namespace, payload.Key, "", payload.ContentType, payload.Metadata)
	if !ok || payload.Size < 0 || payload.Size > objectstorage.MaxSinglePutBytes {
		if ok {
			writeError(w, http.StatusBadRequest, "invalid request")
		}
		return
	}
	checksum, valid := parseChecksum(payload.Checksum)
	if !valid {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	ttl, err := storagev1.PresignTTL(payload.ExpiresIn)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
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
	writeJSON(w, http.StatusOK, presignedRequest(presigned))
}
