package httpapi

import (
	"errors"
	"net/http"

	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/objectstorage"
)

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
	writeError(w, status, message)
}
