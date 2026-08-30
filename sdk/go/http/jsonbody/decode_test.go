package jsonbody

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeAcceptsOneStrictValue(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"value"}`))
	var target struct {
		Name string `json:"name"`
	}
	err := Decode(httptest.NewRecorder(), request, &target, Options{DisallowUnknownFields: true})
	if err != nil || target.Name != "value" {
		t.Fatalf("Decode() error = %v, target = %#v", err, target)
	}
}

func TestDecodeRejectsUnknownField(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"unknown":true}`))
	var target struct {
		Name string `json:"name"`
	}
	if err := Decode(httptest.NewRecorder(), request, &target, Options{DisallowUnknownFields: true}); err == nil {
		t.Fatal("Decode() accepted an unknown field")
	}
}

func TestDecodeRejectsMultipleValues(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{} {}`))
	var target struct{}
	if err := Decode(httptest.NewRecorder(), request, &target, Options{}); !errors.Is(err, ErrMultipleValues) {
		t.Fatalf("Decode() error = %v", err)
	}
}

func TestDecodePreservesMaxBytesError(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"value":"too large"}`))
	var target map[string]string
	err := Decode(httptest.NewRecorder(), request, &target, Options{MaxBytes: 4})
	var maxBytesError *http.MaxBytesError
	if !errors.As(err, &maxBytesError) {
		t.Fatalf("Decode() error = %v", err)
	}
}
