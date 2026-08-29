package loggingadapter

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/gateway"
	sharedlogging "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging"
)

func TestNewStellarmeshValidatesConfiguration(t *testing.T) {
	emitter := &recordingEmitter{accepted: true}
	for _, service := range []string{"", "   ", " gateway", "gateway "} {
		if _, err := NewStellarmesh(StellarmeshConfig{Service: service, Emitter: emitter}); err == nil {
			t.Fatalf("service %q was accepted", service)
		}
	}
	if _, err := NewStellarmesh(StellarmeshConfig{Service: "gateway"}); err == nil {
		t.Fatal("nil emitter was accepted")
	}
	var typedNil *recordingEmitter
	if _, err := NewStellarmesh(StellarmeshConfig{Service: "gateway", Emitter: typedNil}); err == nil {
		t.Fatal("typed-nil emitter was accepted")
	}
}

func TestStellarmeshMapsLevelsAndKeepsTraceExplicit(t *testing.T) {
	emitter := &recordingEmitter{accepted: true}
	logger, err := NewStellarmesh(StellarmeshConfig{Service: "edge-gateway", Emitter: emitter})
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []int{http.StatusNoContent, http.StatusBadRequest, http.StatusBadGateway} {
		if err := logger.Log(context.Background(), gateway.AccessLog{Status: status, RequestID: "request-1"}); err != nil {
			t.Fatal(err)
		}
	}
	wantLevels := []sharedlogging.Level{
		sharedlogging.LevelInfo,
		sharedlogging.LevelWarning,
		sharedlogging.LevelError,
	}
	if len(emitter.events) != len(wantLevels) {
		t.Fatalf("events = %d", len(emitter.events))
	}
	for index, want := range wantLevels {
		event := emitter.events[index]
		if event.Level != want || event.TraceID != "" || event.Metadata["request_id"] != "request-1" {
			t.Fatalf("event %d = %#v", index, event)
		}
	}
}

func TestStellarmeshMapsMetadataAndIdentityPolicy(t *testing.T) {
	startedAt := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	accessLog := gateway.AccessLog{
		Timestamp: startedAt, RequestID: "request-1", Method: http.MethodGet, Path: "/documents",
		Route: "documents", ClientIP: "192.0.2.10", AuthResult: "authenticated",
		UserID: "user-1", Roles: []string{"reader"}, Upstream: "backend",
		Status: http.StatusOK, Elapsed: 1500 * time.Millisecond,
		RateLimitResult: map[gateway.RateLimitScope]string{gateway.RateLimitScopeClientIP: "allowed"},
	}

	withoutIdentity := &recordingEmitter{accepted: true}
	logger, err := NewStellarmesh(StellarmeshConfig{Service: "edge-gateway", Emitter: withoutIdentity})
	if err != nil {
		t.Fatal(err)
	}
	if err := logger.Log(context.Background(), accessLog); err != nil {
		t.Fatal(err)
	}
	event := withoutIdentity.events[0]
	if event.Timestamp != startedAt || event.Service != "edge-gateway" || event.Message != "gateway request" {
		t.Fatalf("event = %#v", event)
	}
	if _, exists := event.Metadata["user_id"]; exists {
		t.Fatalf("identity was included by default: %#v", event.Metadata)
	}
	if event.Metadata["elapsed_milliseconds"] != int64(1500) {
		t.Fatalf("metadata = %#v", event.Metadata)
	}

	withIdentity := &recordingEmitter{accepted: true}
	logger, err = NewStellarmesh(StellarmeshConfig{
		Service: "edge-gateway", Emitter: withIdentity, IncludeIdentity: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := logger.Log(context.Background(), accessLog); err != nil {
		t.Fatal(err)
	}
	if withIdentity.events[0].Metadata["user_id"] != "user-1" {
		t.Fatalf("metadata = %#v", withIdentity.events[0].Metadata)
	}
}

func TestStellarmeshUsesTraceProviderAndSanitizesMetadata(t *testing.T) {
	emitter := &recordingEmitter{accepted: true}
	providerCalled := false
	logger, err := NewStellarmesh(StellarmeshConfig{
		Service: "edge-gateway",
		Emitter: emitter,
		TraceIDProvider: func(_ context.Context, accessLog gateway.AccessLog) string {
			providerCalled = accessLog.RequestID == "request-1"
			return "trace-1"
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := logger.Log(context.Background(), gateway.AccessLog{
		RequestID: "request-1", Path: strings.Repeat("x", 3000), Status: http.StatusOK,
	}); err != nil {
		t.Fatal(err)
	}
	event := emitter.events[0]
	if !providerCalled || event.TraceID != "trace-1" {
		t.Fatalf("provider called = %v, event = %#v", providerCalled, event)
	}
	path, ok := event.Metadata["path"].(string)
	if !ok || len([]rune(path)) >= 3000 || !strings.Contains(path, "[TRUNCATED]") {
		t.Fatalf("path was not sanitized: %#v", event.Metadata["path"])
	}
}

func TestStellarmeshReturnsStableRejectedError(t *testing.T) {
	logger, err := NewStellarmesh(StellarmeshConfig{
		Service: "edge-gateway", Emitter: &recordingEmitter{accepted: false},
	})
	if err != nil {
		t.Fatal(err)
	}
	err = logger.Log(context.Background(), gateway.AccessLog{Status: http.StatusOK})
	if !errors.Is(err, ErrEventRejected) {
		t.Fatalf("error = %v", err)
	}
}

func TestStellarmeshRejectionDoesNotChangeGatewayResponse(t *testing.T) {
	logger, err := NewStellarmesh(StellarmeshConfig{
		Service: "edge-gateway", Emitter: &recordingEmitter{accepted: false},
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := gateway.New(
		gateway.WithRouteResolver(gateway.RouteResolverFunc(func(*http.Request) (gateway.Route, bool, error) {
			return gateway.Route{Name: "public", Upstream: "backend", Access: gateway.AccessPublic}, true, nil
		})),
		gateway.WithUpstreamResolver(gateway.UpstreamResolverFunc(func(gateway.Route) (http.Handler, error) {
			return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			}), nil
		})),
		gateway.WithAccessLogger(logger),
	)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://gateway/public", nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

type recordingEmitter struct {
	accepted bool
	events   []sharedlogging.Event
}

func (emitter *recordingEmitter) Emit(_ context.Context, event sharedlogging.Event) bool {
	emitter.events = append(emitter.events, event)
	return emitter.accepted
}
