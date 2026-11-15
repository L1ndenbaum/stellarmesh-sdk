package gateway

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type trackedResponseBody struct {
	io.Reader
	closed bool
}

func (b *trackedResponseBody) Close() error { b.closed = true; return nil }

func TestResponseHooksAreScopedAndFailClosed(t *testing.T) {
	login := Route{Name: "login", Match: RouteMatch{ExactPath: "/login"}, Upstream: "backend", Access: AccessPublic,
		ResponseHook: func(r *http.Request, status int, h http.Header) (HeaderPolicy, error) {
			cookie, err := r.Cookie("session")
			if err != nil || cookie.Value != "previous" || status != 200 || h.Get("X-Login-User") != "alice" {
				t.Fatalf("invalid hook input: %v %d %v", cookie, status, h)
			}
			h.Set("X-Clone-Test", "private")
			return HeaderPolicy{Add: http.Header{"Set-Cookie": {"session=new; HttpOnly"}}}, nil
		}}
	var body *trackedResponseBody
	backend := Upstream{Name: "backend", URL: "http://backend", RequestHeaders: HeaderPolicy{Remove: []string{"Cookie"}}, ResponseHeaders: HeaderPolicy{Remove: []string{"X-Login-User"}},
		ResponseHook: func(r *http.Request, _ int, _ http.Header) (HeaderPolicy, error) {
			switch r.URL.Query().Get("fault") {
			case "error":
				return HeaderPolicy{}, errors.New("private store failure")
			case "panic":
				panic("private failure")
			case "reserved":
				return HeaderPolicy{Set: http.Header{"X-Request-ID": {"forged"}}}, nil
			}
			return HeaderPolicy{Set: http.Header{"Cache-Control": {"no-store"}}}, nil
		}}
	g, err := New(WithRoutes(login, publicRoute()), WithUpstreams(backend), WithTransport(roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Cookie") != "" {
			t.Fatal("forwarded cookie")
		}
		body = &trackedResponseBody{Reader: strings.NewReader("upstream")}
		return &http.Response{StatusCode: 200, Header: http.Header{"X-Login-User": {"alice"}, "Set-Cookie": {"initial=1"}}, Body: body}, nil
	})))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/login", "/public", "/public?fault=error", "/public?fault=panic", "/public?fault=reserved"} {
		r := httptest.NewRequest("POST", path, nil)
		r.AddCookie(&http.Cookie{Name: "session", Value: "previous"})
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		if !body.closed || w.Header().Get("X-Login-User") != "" || w.Header().Get("X-Clone-Test") != "" {
			t.Fatalf("%s leaked response: %v", path, w.Header())
		}
		switch path {
		case "/login":
			if w.Code != 200 || len(w.Header().Values("Set-Cookie")) != 2 || w.Header().Get("Cache-Control") != "" {
				t.Fatal(w.Result())
			}
		case "/public":
			if w.Code != 200 || len(w.Header().Values("Set-Cookie")) != 1 || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal(w.Result())
			}
		default:
			if w.Code != 503 || w.Header().Get("Set-Cookie") != "" || strings.Contains(w.Body.String(), "private") || strings.Contains(w.Body.String(), "upstream") {
				t.Fatal(w.Result(), w.Body)
			}
		}
	}
}
