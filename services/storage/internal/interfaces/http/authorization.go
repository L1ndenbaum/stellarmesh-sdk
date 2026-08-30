package httpapi

import (
	"net/http"

	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/objectstorage"
	"github.com/L1ndenbaum/stellarmesh-sdk/services/storage/internal/application"
	"github.com/L1ndenbaum/stellarmesh-sdk/services/storage/internal/storagev1"
)

func (handler *Handler) authorize(w http.ResponseWriter, request *http.Request, namespace string, capability storagev1.Capability) bool {
	if err := storagev1.ValidateNamespaceName(namespace); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return false
	}
	switch handler.policy.Authorize(request.Header.Get(storagev1.ServiceTokenHeader), namespace, capability) {
	case storagev1.DecisionAllowed:
		return true
	case storagev1.DecisionUnauthenticated:
		writeError(w, http.StatusUnauthorized, "invalid storage service token")
	default:
		writeError(w, http.StatusForbidden, "storage access forbidden")
	}
	return false
}

func (handler *Handler) storeAndValidate(w http.ResponseWriter, namespaceName, key, versionID, contentType string, metadata map[string]string) (application.Store, objectstorage.Namespace, bool) {
	store, exists := handler.registry.Store(namespaceName)
	namespace, configured := handler.policy.Namespace(namespaceName)
	if !exists || !configured {
		writeError(w, http.StatusForbidden, "storage access forbidden")
		return nil, objectstorage.Namespace{}, false
	}
	if err := storagev1.ValidateObjectInput(namespace, key, versionID, contentType, metadata); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return nil, objectstorage.Namespace{}, false
	}
	return store, namespace, true
}
