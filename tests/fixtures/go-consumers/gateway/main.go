package main

import (
	"context"
	"net/http"
	"strings"

	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/gateway"
	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/gateway/jwtauth"
	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/gateway/redislimit"
	stellarlogging "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging"
	"github.com/redis/go-redis/v9"
)

type emitter struct{}

func (emitter) Emit(context.Context, stellarlogging.Event) bool { return true }

type scriptRunner struct{}

func (scriptRunner) Eval(ctx context.Context, _ string, _ []string, _ ...interface{}) *redis.Cmd {
	return redis.NewCmd(ctx)
}

func main() {
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
		gateway.WithAccessLogEmitter("consumer-gateway", emitter{}),
	)
	if err != nil {
		panic(err)
	}
	var _ http.Handler = handler
}
