package gateway

import (
	"net/http/httptest"
	"testing"
)

func TestIngressGuardsEarlyResponsesAndUpstreams(t *testing.T) {
	g := mustGateway(t, WithIngress(IngressConfig{AllowedPeers: []string{"192.0.2.0/24"}, AllowedHosts: []string{"api.example.test"}, RequireForwardedHTTPS: true}), WithHealth(HealthConfig{}), WithCORS(CORSConfig{AllowedOrigins: []string{"https://ui.example.test"}}))
	for _, tc := range []struct {
		path, peer, host string
		proto            []string
		want             int
	}{
		{"/public", "192.0.2.8:12", "API.example.test", []string{"https"}, 204},
		{"/health/live", "192.0.2.8:12", "api.example.test", []string{"https"}, 200},
		{"/health/live", "192.0.3.1:12", "api.example.test", []string{"https"}, 403},
		{"/public", "192.0.2.8:12", "api.example.test:80", []string{"https"}, 403},
		{"/public", "192.0.2.8:12", "api.example.test", []string{"https", "https"}, 403},
		{"/public", "192.0.2.8:12", "api.example.test", nil, 403},
		{"/preflight", "192.0.3.1:12", "api.example.test", []string{"https"}, 403},
	} {
		r := httptest.NewRequest("GET", tc.path, nil)
		if tc.path == "/preflight" {
			r.Method = "OPTIONS"
			r.Header.Set("Origin", "https://ui.example.test")
			r.Header.Set("Access-Control-Request-Method", "GET")
		}
		r.Host = tc.host
		r.RemoteAddr = tc.peer
		r.Header.Set("X-Forwarded-For", "192.0.2.8")
		r.Header["X-Forwarded-Proto"] = tc.proto
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Errorf("%+v got %d", tc, w.Code)
		}
	}
	_, err := New(WithRoutes(publicRoute()), WithUpstreams(Upstream{Name: "backend", URL: "http://backend"}), WithIngress(IngressConfig{AllowedHosts: []string{"api.example.test"}, RequireForwardedHTTPS: true}))
	if err == nil {
		t.Fatal("expected rejection for untrusted forwarded HTTPS")
	}
}
