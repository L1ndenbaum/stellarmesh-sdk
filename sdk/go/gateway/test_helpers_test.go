package gateway

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

func recordingLimiter(name string, appendStage func(string)) RateLimiter {
	return RateLimiterFunc(func(context.Context, RateLimitRequest) (RateLimitDecision, error) {
		appendStage(name)
		return RateLimitDecision{Allowed: true}, nil
	})
}

func mustGateway(t *testing.T, options ...Option) *Gateway {
	t.Helper()
	base := []Option{
		WithRoutes(publicRoute()),
		withTestUpstreams(map[string]http.Handler{"backend": http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})}),
	}
	base = append(base, options...)
	gateway, err := New(base...)
	if err != nil {
		t.Fatal(err)
	}
	return gateway
}

func publicRoute() Route {
	return Route{Name: "public", Match: RouteMatch{ExactPath: "/public"}, Upstream: "backend", Access: AccessPublic}
}

func withTestUpstreams(upstreams map[string]http.Handler) Option {
	return WithUpstreamResolver(UpstreamResolverFunc(func(route Route) (http.Handler, error) {
		handler, ok := upstreams[route.Upstream]
		if !ok {
			return nil, errors.New("missing test upstream")
		}
		return handler, nil
	}))
}
