package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/gateway"
	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/gateway/jwtauth"
	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/gateway/redislimit"
	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/gateway/sessionauth"
	"github.com/redis/go-redis/v9"
)

type scriptRunner struct{}

func (scriptRunner) Eval(ctx context.Context, _ string, _ []string, _ ...interface{}) *redis.Cmd {
	return redis.NewCmd(ctx)
}

func main() {
	sessions, err := sessionauth.NewStore(sessionauth.StoreConfig{Client: scriptRunner{}, ProjectScope: "consumer"})
	if err != nil {
		panic(err)
	}
	sessionAuthenticator, err := sessionauth.NewAuthenticator(sessions)
	if err != nil {
		panic(err)
	}
	cookie, err := gateway.CookieCredential("sid")
	if err != nil {
		panic(err)
	}
	keys, err := sessionauth.NewKeyBuilder(sessionauth.KeyConfig{})
	if err != nil {
		panic(err)
	}
	if key, err := keys.SessionKey("consumer", "example"); err != nil || key != "consumer:session:example" {
		panic("session key contract")
	}
	if key, err := keys.UserSessionsKey("consumer", "user"); err != nil || key != "consumer:user_sessions:user" {
		panic("session user index contract")
	}
	sessionHandler, err := gateway.New(
		gateway.WithRoutes(gateway.Route{Name: "session", Match: gateway.RouteMatch{ExactPath: "/"}, Upstream: "backend"}),
		gateway.WithUpstreams(gateway.Upstream{Name: "backend", URL: "http://127.0.0.1:8080"}),
		gateway.WithAuthenticator(sessionAuthenticator, cookie),
	)
	if err != nil {
		panic(err)
	}
	var _ http.Handler = sessionHandler
	var _ sessionauth.SessionReader = sessions
	var _ gateway.Authenticator = sessionAuthenticator
	var _ gateway.CredentialExtractor = gateway.BearerCredential()
	var _ func(context.Context, sessionauth.CreateOptions) (sessionauth.Session, error) = sessions.Create
	var _ func(context.Context, string, time.Duration) (sessionauth.Session, bool, error) = sessions.Renew
	var _ func(context.Context, string) error = sessions.Revoke
	var _ func(context.Context, string) ([]sessionauth.Session, error) = sessions.ListByUser
	var _ func(context.Context, string) error = sessions.RevokeByUser
	var _ error = sessionauth.ErrCorruptSession
	var _ error = sessionauth.ErrSessionCollision
	var _ error = gateway.ErrInvalidCredential

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
