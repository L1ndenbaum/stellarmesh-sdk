package gateway

import (
	"bufio"
	"net"
	"net/http"
	"testing"
)

func TestResponseRecorderPreservesResponseControllerCapabilities(t *testing.T) {
	underlying := &controllerWriter{header: make(http.Header)}
	recorder := newResponseRecorder(underlying)
	controller := http.NewResponseController(recorder)
	if err := controller.Flush(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := controller.Hijack(); err != nil {
		t.Fatal(err)
	}
	if !underlying.flushed || !underlying.hijacked {
		t.Fatalf("flushed = %v, hijacked = %v", underlying.flushed, underlying.hijacked)
	}
}

type controllerWriter struct {
	header   http.Header
	flushed  bool
	hijacked bool
}

func (writer *controllerWriter) Header() http.Header { return writer.header }

func (writer *controllerWriter) WriteHeader(int) {}

func (writer *controllerWriter) Write(body []byte) (int, error) { return len(body), nil }

func (writer *controllerWriter) Flush() { writer.flushed = true }

func (writer *controllerWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	writer.hijacked = true
	return nil, nil, nil
}
