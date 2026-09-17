package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGatewayRejectsProtectedRoutesWithoutAuthenticatorAtConstruction(t *testing.T) {
	_, err := New(
		WithRoutes(Route{Name: "protected", Match: RouteMatch{ExactPath: "/private"}, Upstream: "backend"}),
		withTestUpstreams(map[string]http.Handler{"backend": http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})}),
	)
	if err == nil || !strings.Contains(err.Error(), "authenticator") {
		t.Fatalf("error = %v", err)
	}
}

func TestGatewayDynamicProtectedRouteWithoutAuthenticatorFailsClosed(t *testing.T) {
	gateway, err := New(
		WithRouteResolver(RouteResolverFunc(func(*http.Request) (Route, bool, error) {
			return Route{Name: "dynamic", Upstream: "backend", Access: AccessProtected}, true, nil
		})),
		withTestUpstreams(map[string]http.Handler{"backend": http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Fatal("request reached upstream")
		})}),
	)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "http://gateway/dynamic", nil)
	request.Header.Set("Authorization", "Bearer token")
	gateway.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", response.Code)
	}
}
