// Package jsonbody 提供协议中立的有界 JSON 请求解码。
package jsonbody

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

// DefaultMaxBytes 是未显式设置请求体上限时使用的默认值。
const DefaultMaxBytes int64 = 1 << 20

// ErrMultipleValues 表示请求体包含多个 JSON 值。
var ErrMultipleValues = errors.New("request body must contain exactly one JSON value")

// Options 控制有界 JSON 解码。
type Options struct {
	MaxBytes              int64
	DisallowUnknownFields bool
}

// Decode 只负责解码并返回错误，HTTP 响应格式由调用方拥有。
func Decode(w http.ResponseWriter, request *http.Request, target any, options Options) error {
	maxBytes := options.MaxBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	body := http.MaxBytesReader(w, request.Body, maxBytes)
	defer body.Close()

	decoder := json.NewDecoder(body)
	if options.DisallowUnknownFields {
		decoder.DisallowUnknownFields()
	}
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err != nil {
			return err
		}
		return ErrMultipleValues
	}
	return nil
}
