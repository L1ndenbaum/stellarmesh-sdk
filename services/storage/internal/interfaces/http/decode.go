package httpapi

import (
	"errors"
	"net/http"

	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/http/jsonbody"
	"github.com/L1ndenbaum/stellarmesh-sdk/services/storage/internal/storagev1"
)

func decode(w http.ResponseWriter, request *http.Request, target any) bool {
	err := jsonbody.Decode(w, request, target, jsonbody.Options{
		MaxBytes: storagev1.MaxControlBodyBytes, DisallowUnknownFields: true,
	})
	if err == nil {
		return true
	}
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
	} else {
		writeError(w, http.StatusBadRequest, "invalid request")
	}
	return false
}
