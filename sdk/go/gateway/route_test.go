package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPatternRoutesThroughGateway(t *testing.T) {
	route := func(name string, match RouteMatch) Route {
		return Route{Name: name, Match: match, Access: AccessPublic, Upstream: "api"}
	}
	handler, err := New(WithRoutes(
		route("fixed", RouteMatch{ExactPaths: []string{"/objects/known", "/objects/list"}}),
		route("item", RouteMatch{PathTemplate: "/objects/{id}", Priority: 2, Methods: []string{"GET"}}),
		route("tree", RouteMatch{PathTemplate: "/objects/**"}),
		route("one", RouteMatch{PathTemplate: "/one/*"}),
		route("order", RouteMatch{PathRegexp: `/orders/cs_[a-z]{2,8}`}),
		route("fallback", RouteMatch{PathPrefix: "/objects/"}),
		route("conflict", RouteMatch{PathRegexp: `/objects/special`, Priority: 2}),
	), withTestUpstreams(map[string]http.Handler{"api": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route, _ := RouteFromContext(r.Context())
		io.WriteString(w, route.Name+" "+r.PathValue("id"))
	})}))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, path string
		status       int
		body         string
	}{
		{"GET", "/objects", 200, "tree "}, {"GET", "/objects/", 200, "tree "}, {"GET", "/objects/a/b", 200, "tree "},
		{"GET", "/objects/known", 200, "fixed "}, {"GET", "/objects/a%20b", 200, "item a b"}, {"POST", "/objects/id", 200, "tree "},
		{"GET", "/orders/cs_ab", 200, "order "}, {"GET", "/orders/cs_ab/extra", 404, ""}, {"GET", "/x/orders/cs_ab", 404, ""},
		{"GET", "/one/a", 200, "one "}, {"GET", "/one/", 404, ""}, {"GET", "/one/a/b", 404, ""},
		{"GET", "/objects/a%2fb", 404, ""}, {"GET", "/objects/a%5cb", 404, ""}, {"GET", "/objects/%2e%2e/private", 404, ""},
		{"GET", "/objects//admin", 404, ""}, {"GET", "/objects/special", 503, ""},
	} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		if w.Code != tc.status || tc.status == 200 && w.Body.String() != tc.body {
			t.Errorf("%s %s: %d %q", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
}

func TestInvalidPathMatchersFailConstruction(t *testing.T) {
	for _, match := range []RouteMatch{
		{PathTemplate: "/a/**/b"}, {PathTemplate: "/a/{x}/{x}"}, {PathTemplate: "/a/{a-b}"},
		{PathRegexp: "("}, {PathTemplate: "/a/*", ExactPath: "/a"}, {ExactPaths: []string{"/a", "/a"}},
	} {
		_, err := New(WithRoutes(Route{Name: "api", Upstream: "api", Match: match, Access: AccessPublic}), WithUpstreams(Upstream{Name: "api", URL: "http://api"}))
		if err == nil {
			t.Fatal(match)
		}
	}
}

func TestStaticRoutesPreferExactThenLongestPrefix(t *testing.T) {
	resolver, err := newStaticRouteResolver([]Route{
		{Name: "root", Match: RouteMatch{PathPrefix: "/api/"}, Upstream: "root", Access: AccessPublic},
		{Name: "nested", Match: RouteMatch{PathPrefix: "/api/v1/"}, Upstream: "nested", Access: AccessPublic},
		{Name: "exact", Match: RouteMatch{ExactPath: "/api/v1/items"}, Upstream: "exact", Access: AccessPublic},
	})
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{
		"/api/v1/items": "exact",
		"/api/v1/other": "nested",
		"/api/v2/items": "root",
	} {
		route, found, resolveErr := resolver.Resolve(httptest.NewRequest(http.MethodGet, path, nil))
		if resolveErr != nil || !found || route.Name != want {
			t.Fatalf("Resolve(%q) = %#v, %v, %v; want %q", path, route, found, resolveErr, want)
		}
	}
}
