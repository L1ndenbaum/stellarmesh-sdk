package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

// DefaultJSONBodyLimit is the shared default maximum JSON request size.
const DefaultJSONBodyLimit int64 = 1 << 20

// DecodeJSONOptions controls bounded JSON decoding.
type DecodeJSONOptions struct {
	MaxBytes              int64
	DisallowUnknownFields bool
}

// DecodeJSON decodes a bounded JSON body and writes an error response on failure.
func DecodeJSON(w http.ResponseWriter, r *http.Request, target any, maxBytes int64) bool {
	if err := DecodeJSONWithOptions(w, r, target, DecodeJSONOptions{MaxBytes: maxBytes}); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			WriteError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return false
		}
		WriteError(w, http.StatusBadRequest, err.Error())
		return false
	}
	return true
}

// DecodeJSONWithOptions returns decoding errors to the caller.
func DecodeJSONWithOptions(w http.ResponseWriter, r *http.Request, target any, options DecodeJSONOptions) error {
	maxBytes := options.MaxBytes
	if maxBytes <= 0 {
		maxBytes = DefaultJSONBodyLimit
	}
	body := http.MaxBytesReader(w, r.Body, maxBytes)
	defer body.Close()

	decoder := json.NewDecoder(body)
	if options.DisallowUnknownFields {
		decoder.DisallowUnknownFields()
	}
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request body must contain one JSON value")
		}
		return err
	}
	return nil
}

// DecodeJSONStrict rejects unknown fields in addition to enforcing the body limit.
func DecodeJSONStrict(w http.ResponseWriter, r *http.Request, target any, maxBytes int64) error {
	return DecodeJSONWithOptions(w, r, target, DecodeJSONOptions{
		MaxBytes:              maxBytes,
		DisallowUnknownFields: true,
	})
}
