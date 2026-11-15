package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIndependentUpstreamHeaderPolicies(t *testing.T) {
	overrides := http.Header{"X-Forwarded-Proto": {"https"}, "X-Forwarded-Port": {"443"}}
	var got *http.Request
	g, err := New(WithRoutes(Route{Name: "signed", Match: RouteMatch{PathPrefix: "/files/"}, Access: AccessPublic, Upstream: "objects"}, publicRoute()), WithUpstreams(
		Upstream{Name: "objects", URL: "http://storage:9000", Host: "files.example.test", RequestHeaders: HeaderPolicy{Set: overrides, Remove: []string{"Cookie"}},
			ResponseHeaders: HeaderPolicy{Set: http.Header{"Cache-Control": {"no-store"}}, Add: http.Header{"Set-Cookie": {"second=2"}}, Remove: []string{"X-Private"}}},
		Upstream{Name: "backend", URL: "http://backend"}),
		WithTransport(roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			got = r
			return &http.Response{StatusCode: 204, Header: http.Header{"Set-Cookie": {"first=1"}, "X-Private": {"private"}}, Body: http.NoBody}, nil
		})))
	if err != nil {
		t.Fatal(err)
	}
	overrides.Set("X-Forwarded-Proto", "mutated")
	for _, p := range []string{"/files/a%20b?sig=x%2By", "/public"} {
		r := httptest.NewRequest("GET", p, nil)
		r.Header.Set("Cookie", "local=1")
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		if w.Code != 204 {
			t.Fatal(w.Code)
		}
		if p == "/public" {
			if got.Host != "backend" || got.Header.Get("Cookie") == "" || w.Header().Get("X-Private") == "" {
				t.Fatalf("unrelated upstream: %v %v", got, w.Header())
			}
		} else if got.Host != "files.example.test" || got.URL.Host != "storage:9000" || got.URL.RequestURI() != p || got.Header.Get("Cookie") != "" ||
			got.Header.Get("X-Forwarded-Proto") != "https" || got.Header.Get("X-Forwarded-Port") != "443" || w.Header().Get("X-Private") != "" ||
			w.Header().Get("Cache-Control") != "no-store" || len(w.Header().Values("Set-Cookie")) != 2 {
			t.Fatalf("signed upstream: %v %v", got, w.Header())
		}
	}
}

func TestUpstreamRejectsInvalidOverrides(t *testing.T) {
	for _, key := range []string{"Connection", "X-Request-ID", "X-User-ID", "X-Forwarded-For", "X-Forwarded-Proto", "Host", "Content-Length", "Bad Field"} {
		_, err := New(WithRoutes(publicRoute()), WithUpstreams(Upstream{Name: "backend", URL: "http://backend",
			RequestHeaders: HeaderPolicy{Set: http.Header{key: {"spoof"}}}}))
		if err == nil {
			t.Fatal(key)
		}
	}
}
