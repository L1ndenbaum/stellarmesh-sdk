package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/gateway"
	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/gateway/jwtauth"
	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/gateway/redislimit"
	"github.com/redis/go-redis/v9"
)

type scriptRunner struct{}

func (scriptRunner) Eval(ctx context.Context, _ string, _ []string, _ ...interface{}) *redis.Cmd {
	return redis.NewCmd(ctx)
}

func main() {
	defaultHandler, err := gateway.New(
		gateway.WithRoutes(gateway.Route{
			Name: "default", Match: gateway.RouteMatch{ExactPath: "/default"},
			Upstream: "backend", Access: gateway.AccessPublic,
		}),
		gateway.WithUpstreams(gateway.Upstream{Name: "backend", URL: "http://127.0.0.1:8080"}),
	)
	if err != nil {
		panic(err)
	}

	authenticator, err := jwtauth.New(jwtauth.Config{
		Secret:   []byte(strings.Repeat("s", 32)),
		Issuer:   "consumer",
		Audience: "consumer-api",
	})
	if err != nil {
		panic(err)
	}
	limiter, err := redislimit.New(redislimit.Config{
		Client: scriptRunner{}, Scope: gateway.RateLimitScopeClientIP,
		KeyPrefix: "consumer", RatePerSecond: 10, Burst: 20,
	})
	if err != nil {
		panic(err)
	}
	handler, err := gateway.New(
		gateway.WithRoutes(gateway.Route{
			Name: "public", Match: gateway.RouteMatch{ExactPath: "/"},
			Upstream: "backend", Access: gateway.AccessPublic,
		}),
		gateway.WithUpstreams(gateway.Upstream{Name: "backend", URL: "http://127.0.0.1:8080"}),
		gateway.WithAuthenticator(authenticator),
		gateway.WithClientIPRateLimiter(limiter),
		gateway.WithSlogAccessLogger(gateway.SlogAccessLoggerConfig{
			Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		}),
		gateway.WithErrorResponder(gateway.ErrorResponderFunc(func(w http.ResponseWriter, _ *http.Request, gatewayError gateway.GatewayError) {
			http.Error(w, gatewayError.Code, gatewayError.Status)
		})),
		gateway.WithHealth(gateway.HealthConfig{
			Service: "consumer-gateway",
			Responder: gateway.HealthResponderFunc(func(w http.ResponseWriter, _ *http.Request, result gateway.HealthResult) {
				if result.Kind != gateway.HealthKindLive && result.Kind != gateway.HealthKindReady {
					panic("unknown health kind")
				}
				w.WriteHeader(http.StatusOK)
			}),
		}),
	)
	if err != nil {
		panic(err)
	}
	withoutAccessLog, err := gateway.New(
		gateway.WithRoutes(gateway.Route{
			Name: "quiet", Match: gateway.RouteMatch{ExactPath: "/quiet"},
			Upstream: "backend", Access: gateway.AccessPublic,
		}),
		gateway.WithUpstreams(gateway.Upstream{Name: "backend", URL: "http://127.0.0.1:8080"}),
		gateway.WithoutAccessLog(),
	)
	if err != nil {
		panic(err)
	}
	customAccessLog, err := gateway.New(
		gateway.WithRoutes(gateway.Route{
			Name: "custom", Match: gateway.RouteMatch{ExactPath: "/custom"},
			Upstream: "backend", Access: gateway.AccessPublic,
		}),
		gateway.WithUpstreams(gateway.Upstream{Name: "backend", URL: "http://127.0.0.1:8080"}),
		gateway.WithAccessLogger(gateway.AccessLoggerFunc(func(context.Context, gateway.AccessLog) error {
			return nil
		})),
	)
	if err != nil {
		panic(err)
	}
	var _ http.Handler = defaultHandler
	var _ http.Handler = handler
	var _ http.Handler = withoutAccessLog
	var _ http.Handler = customAccessLog
}
