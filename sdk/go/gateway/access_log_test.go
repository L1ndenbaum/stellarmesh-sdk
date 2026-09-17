package gateway

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequestAccessLogPreservesRateLimitDecisions(t *testing.T) {
	for _, item := range []struct {
		name    string
		limiter RateLimiter
		status  int
		want    map[RateLimitScope]string
	}{
		{name: "disabled", status: http.StatusNoContent, want: map[RateLimitScope]string{RateLimitScopeClientIP: "disabled", RateLimitScopeUpstream: "disabled"}},
		{name: "allowed", limiter: RateLimiterFunc(func(context.Context, RateLimitRequest) (RateLimitDecision, error) {
			return RateLimitDecision{Allowed: true}, nil
		}), status: http.StatusNoContent, want: map[RateLimitScope]string{RateLimitScopeClientIP: "allowed", RateLimitScopeUpstream: "disabled"}},
		{name: "rejected", limiter: RateLimiterFunc(func(context.Context, RateLimitRequest) (RateLimitDecision, error) {
			return RateLimitDecision{Allowed: false}, nil
		}), status: http.StatusTooManyRequests, want: map[RateLimitScope]string{RateLimitScopeClientIP: "rejected"}},
		{name: "error", limiter: RateLimiterFunc(func(context.Context, RateLimitRequest) (RateLimitDecision, error) {
			return RateLimitDecision{}, errors.New("unavailable")
		}), status: http.StatusServiceUnavailable, want: map[RateLimitScope]string{RateLimitScopeClientIP: "error"}},
	} {
		t.Run(item.name, func(t *testing.T) {
			var logs []AccessLog
			options := []Option{
				WithRoutes(publicRoute()),
				withTestUpstreams(map[string]http.Handler{"backend": http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })}),
				WithAccessLogger(AccessLoggerFunc(func(_ context.Context, event AccessLog) error { logs = append(logs, event); return nil })),
			}
			if item.limiter != nil {
				options = append(options, WithClientIPRateLimiter(item.limiter))
			}
			gateway, err := New(options...)
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			gateway.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://gateway/public", nil))
			if response.Code != item.status || len(logs) != 1 {
				t.Fatalf("status = %d, logs = %d", response.Code, len(logs))
			}
			if !maps.Equal(logs[0].RateLimitResult, item.want) {
				t.Fatalf("rate limits = %#v, want %#v", logs[0].RateLimitResult, item.want)
			}
		})
	}
}

func TestAccessLogCopyOwnsMutableFields(t *testing.T) {
	original := AccessLog{Roles: []string{"reader"}, RateLimitResult: map[RateLimitScope]string{RateLimitScopeClientIP: "allowed"}}
	copied := cloneAccessLog(original)
	copied.Roles[0] = "changed"
	copied.RateLimitResult[RateLimitScopeClientIP] = "changed"
	if original.Roles[0] != "reader" || original.RateLimitResult[RateLimitScopeClientIP] != "allowed" {
		t.Fatalf("original mutated: %#v", original)
	}
}

func TestWithoutAccessLogDisablesDefaultLogger(t *testing.T) {
	previous := slog.Default()
	handler := &recordingSlogHandler{}
	slog.SetDefault(slog.New(handler))
	t.Cleanup(func() { slog.SetDefault(previous) })

	gateway, err := New(
		WithRoutes(publicRoute()),
		WithoutAccessLog(),
		withTestUpstreams(map[string]http.Handler{"backend": http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})}),
	)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	gateway.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://gateway/public", nil))
	if response.Code != http.StatusNoContent || len(handler.snapshot()) != 0 {
		t.Fatalf("status = %d, records = %d", response.Code, len(handler.snapshot()))
	}
}

func TestAccessLogFailureIsObservedWithoutChangingResponse(t *testing.T) {
	handler := &recordingSlogHandler{err: errors.New("writer unavailable")}
	observations := make([]Observation, 0, 2)
	gateway, err := New(
		WithRoutes(publicRoute()),
		WithSlogAccessLogger(SlogAccessLoggerConfig{Logger: slog.New(handler)}),
		WithObserver(ObserverFunc(func(_ context.Context, observation Observation) {
			observations = append(observations, observation)
		})),
		withTestUpstreams(map[string]http.Handler{"backend": http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})}),
	)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	gateway.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://gateway/public", nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d", response.Code)
	}
	if len(observations) != 2 || observations[0].Kind != ObservationAccessLogFailure || observations[1].Kind != ObservationRequestCompleted {
		t.Fatalf("observations = %#v", observations)
	}
}

func TestAccessLoggerPanicIsObservedWithoutChangingResponse(t *testing.T) {
	observations := make([]Observation, 0, 2)
	gateway, err := New(
		WithRoutes(publicRoute()),
		WithAccessLogger(AccessLoggerFunc(func(context.Context, AccessLog) error {
			panic("writer failed")
		})),
		WithObserver(ObserverFunc(func(_ context.Context, observation Observation) {
			observations = append(observations, observation)
		})),
		withTestUpstreams(map[string]http.Handler{"backend": http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})}),
	)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	gateway.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://gateway/public", nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d", response.Code)
	}
	if len(observations) != 2 || observations[0].Kind != ObservationAccessLogFailure {
		t.Fatalf("observations = %#v", observations)
	}
}
