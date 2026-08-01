package headers

import (
	"net/http/httptest"
	"testing"
)

func TestClientIPPrefersForwardedHeader(t *testing.T) {
	request := httptest.NewRequest("GET", "/", nil)
	request.RemoteAddr = "10.0.0.1:1234"
	request.Header.Set(HeaderXForwardedFor, "203.0.113.1, 10.0.0.1")
	if got := ClientIP(request); got != "203.0.113.1" {
		t.Fatalf("ClientIP() = %q", got)
	}
}
