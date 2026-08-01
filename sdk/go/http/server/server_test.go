package server

import (
	"net/http"
	"testing"
	"time"
)

func TestNewCopiesConfig(t *testing.T) {
	handler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	got := New(Config{Addr: ":9000", ReadTimeout: time.Second}, handler)
	if got.Addr != ":9000" || got.ReadTimeout != time.Second || got.Handler == nil {
		t.Fatalf("server = %#v", got)
	}
}
