package gateway

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRouteTimeoutProfiles(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/header":
			<-r.Context().Done()
		case "/idle":
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, "data: partial\n\n")
			http.NewResponseController(w).Flush()
			<-r.Context().Done()
		case "/active":
			for range 5 {
				io.WriteString(w, "chunk")
				http.NewResponseController(w).Flush()
				time.Sleep(50 * time.Millisecond)
			}
		}
	}))
	defer upstream.Close()
	route := Route{Name: "stream", Match: RouteMatch{ExactPaths: []string{"/header", "/idle", "/active"}}, Upstream: "api", TimeoutProfile: "stream", Access: AccessPublic}
	h, err := New(WithRoutes(route), WithUpstreams(Upstream{Name: "api", URL: upstream.URL}), WithTimeoutProfiles(TimeoutProfile{Name: "stream", ResponseHeaderTimeout: 150 * time.Millisecond, ResponseBodyIdleTimeout: 200 * time.Millisecond}))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(h)
	defer server.Close()
	c := server.Client()
	c.Timeout = 4 * time.Second
	for _, path := range route.Match.ExactPaths {
		resp, err := c.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		switch path {
		case "/header":
			if resp.StatusCode != 504 || err != nil {
				t.Fatal(resp.StatusCode, err)
			}
		case "/idle":
			if string(body) != "data: partial\n\n" || !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatal(string(body), err)
			}
		case "/active":
			if string(body) != strings.Repeat("chunk", 5) || err != nil {
				t.Fatal(string(body), err)
			}
		}
	}
}

func TestUnknownTimeoutProfileRejected(t *testing.T) {
	route := publicRoute()
	route.TimeoutProfile = "missing"
	_, err := New(WithRoutes(route), WithUpstreams(Upstream{Name: "backend", URL: "http://backend"}))
	if err == nil {
		t.Fatal("expected invalid profile")
	}
}
