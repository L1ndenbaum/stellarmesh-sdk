package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

// DefaultJSONBodyLimit 是共享的默认 JSON 请求大小上限。
const DefaultJSONBodyLimit int64 = 1 << 20

// DecodeJSONOptions 控制有界 JSON 解码。
type DecodeJSONOptions struct {
	MaxBytes              int64
	DisallowUnknownFields bool
}

// DecodeJSON 解码有大小限制的 JSON 请求体，并在失败时写入错误响应。
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

// DecodeJSONWithOptions 将解码错误返回给调用方。
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

// DecodeJSONStrict 在限制请求体大小的同时拒绝未知字段。
func DecodeJSONStrict(w http.ResponseWriter, r *http.Request, target any, maxBytes int64) error {
	return DecodeJSONWithOptions(w, r, target, DecodeJSONOptions{
		MaxBytes:              maxBytes,
		DisallowUnknownFields: true,
	})
}
