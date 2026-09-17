package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

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
