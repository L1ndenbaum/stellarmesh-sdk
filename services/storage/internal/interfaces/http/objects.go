package httpapi

import (
	"net/http"

	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/objectstorage"
	"github.com/L1ndenbaum/stellarmesh-sdk/services/storage/internal/storagev1"
)

// HandleStat 读取对象元数据。
func (handler *Handler) HandleStat(w http.ResponseWriter, request *http.Request) {
	var payload storagev1.ObjectRequest
	if !decode(w, request, &payload) || !handler.authorize(w, request, payload.Namespace, storagev1.CapabilityRead) {
		return
	}
	store, _, ok := handler.storeAndValidate(w, payload.Namespace, payload.Key, payload.VersionID, "", nil)
	if !ok {
		return
	}
	info, err := store.Stat(request.Context(), objectstorage.ObjectRef{Key: payload.Key, VersionID: payload.VersionID})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, objectInfo(info))
}

// HandleDelete 删除当前对象语义或指定版本。
func (handler *Handler) HandleDelete(w http.ResponseWriter, request *http.Request) {
	var payload storagev1.ObjectRequest
	if !decode(w, request, &payload) || !handler.authorize(w, request, payload.Namespace, storagev1.CapabilityDelete) {
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
	writeJSON(w, http.StatusOK, map[string]any{})
}
