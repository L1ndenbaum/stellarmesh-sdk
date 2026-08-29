package gateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDefaultSlogAccessLoggerUsesCurrentDefaultAndOmitsSensitiveData(t *testing.T) {
	previous := slog.Default()
	handler := &recordingSlogHandler{}
	t.Cleanup(func() { slog.SetDefault(previous) })

	gateway, err := New(
		WithRoutes(publicRoute()),
		withTestUpstreams(map[string]http.Handler{"backend": http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})}),
	)
	if err != nil {
		t.Fatal(err)
	}
	// 默认 Logger 在请求完成时解析，允许项目在构造 Gateway 后统一替换 slog.Default。
	slog.SetDefault(slog.New(handler))
	request := httptest.NewRequest(http.MethodGet, "http://gateway/public?token=super-secret", nil)
	request.Header.Set("Authorization", "Bearer credential")
	request.Header.Set("Cookie", "session=credential")
	response := httptest.NewRecorder()
	gateway.ServeHTTP(response, request)
	records := handler.snapshot()
	if response.Code != http.StatusNoContent || len(records) != 1 {
		t.Fatalf("status = %d, records = %d", response.Code, len(records))
	}
	record := records[0]
	if record.Level != slog.LevelInfo || record.Message != accessLogMessage {
		t.Fatalf("record = %#v", record)
	}
	attributes := slogRecordAttributes(record)
	if attributes["path"] != "/public" || attributes["client_ip"] != "192.0.2.1" {
		t.Fatalf("attributes = %#v", attributes)
	}
	if _, exists := attributes["user_id"]; exists {
		t.Fatalf("默认访问日志不应包含身份字段: %#v", attributes)
	}
	serialized := fmt.Sprint(attributes)
	if strings.Contains(serialized, "super-secret") || strings.Contains(serialized, "credential") {
		t.Fatalf("访问日志泄露了请求凭据: %s", serialized)
	}
}

func TestSlogAccessLoggerLevelsAndIdentity(t *testing.T) {
	handler := &recordingSlogHandler{}
	logger := NewSlogAccessLogger(SlogAccessLoggerConfig{
		Logger:          slog.New(handler),
		IncludeIdentity: true,
	})
	for _, status := range []int{http.StatusNoContent, http.StatusBadRequest, http.StatusBadGateway} {
		err := logger.Log(context.Background(), AccessLog{
			Status: status, UserID: "user-1", Roles: []string{"admin"},
			RateLimitResult: map[RateLimitScope]string{RateLimitScopeClientIP: "allowed"},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	records := handler.snapshot()
	wantLevels := []slog.Level{slog.LevelInfo, slog.LevelWarn, slog.LevelError}
	if len(records) != len(wantLevels) {
		t.Fatalf("records = %d", len(records))
	}
	for index, want := range wantLevels {
		if records[index].Level != want {
			t.Fatalf("record %d level = %v, want %v", index, records[index].Level, want)
		}
	}
	attributes := slogRecordAttributes(records[0])
	if attributes["user_id"] != "user-1" {
		t.Fatalf("attributes = %#v", attributes)
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

func TestAccessLogOptionsAreMutuallyExclusive(t *testing.T) {
	custom := AccessLoggerFunc(func(context.Context, AccessLog) error { return nil })
	testCases := [][]Option{
		{WithAccessLogger(custom), WithSlogAccessLogger(SlogAccessLoggerConfig{})},
		{WithSlogAccessLogger(SlogAccessLoggerConfig{}), WithoutAccessLog()},
		{WithoutAccessLog(), WithAccessLogger(custom)},
	}
	for index, options := range testCases {
		if _, err := New(options...); err == nil || !strings.Contains(err.Error(), "duplicate gateway component: access_logger") {
			t.Fatalf("case %d error = %v", index, err)
		}
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

func TestObserverPanicDoesNotAffectResponse(t *testing.T) {
	gateway, err := New(
		WithRoutes(publicRoute()),
		WithObserver(ObserverFunc(func(context.Context, Observation) { panic("observer failed") })),
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
}

func TestHealthEndpointsBypassRoutesAndSkipSuccessfulAccessLog(t *testing.T) {
	accessLogs := make([]AccessLog, 0, 2)
	proxied := false
	gateway, err := New(
		WithRoutes(publicRoute()),
		WithHealth(HealthConfig{Service: "edge-gateway"}),
		WithAccessLogger(AccessLoggerFunc(func(_ context.Context, accessLog AccessLog) error {
			accessLogs = append(accessLogs, accessLog)
			return nil
		})),
		withTestUpstreams(map[string]http.Handler{"backend": http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			proxied = true
		})}),
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/health/live", "/health/ready"} {
		response := httptest.NewRecorder()
		gateway.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://gateway"+path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("%s status = %d, body = %s", path, response.Code, response.Body.String())
		}
	}
	if proxied || len(accessLogs) != 0 {
		t.Fatalf("proxied = %v, access logs = %d", proxied, len(accessLogs))
	}
}

func TestReadinessFailureReturns503AndIsObserved(t *testing.T) {
	accessLogs := make([]AccessLog, 0, 1)
	observations := make([]Observation, 0, 2)
	gateway, err := New(
		WithRoutes(publicRoute()),
		WithHealth(HealthConfig{
			Readiness: ReadinessCheckerFunc(func(context.Context) error { return errors.New("dependency unavailable") }),
		}),
		WithAccessLogger(AccessLoggerFunc(func(_ context.Context, accessLog AccessLog) error {
			accessLogs = append(accessLogs, accessLog)
			return nil
		})),
		WithObserver(ObserverFunc(func(_ context.Context, observation Observation) {
			observations = append(observations, observation)
		})),
		withTestUpstreams(map[string]http.Handler{"backend": http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})}),
	)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	gateway.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://gateway/health/ready", nil))
	if response.Code != http.StatusServiceUnavailable || len(accessLogs) != 1 {
		t.Fatalf("status = %d, access logs = %d", response.Code, len(accessLogs))
	}
	if got := accessLogs[0].ErrorCode; got != "readiness_failed" {
		t.Fatalf("error_code = %#v", got)
	}
	if len(observations) != 2 || observations[0].Kind != ObservationComponentFailure || observations[0].Component != "readiness_failed" {
		t.Fatalf("observations = %#v", observations)
	}
}

func TestReadinessTimeoutFailsClosed(t *testing.T) {
	gateway, err := New(
		WithRoutes(publicRoute()),
		WithHealth(HealthConfig{
			CheckTimeout: time.Millisecond,
			Readiness: ReadinessCheckerFunc(func(ctx context.Context) error {
				<-ctx.Done()
				return ctx.Err()
			}),
		}),
		withTestUpstreams(map[string]http.Handler{"backend": http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})}),
	)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	gateway.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://gateway/health/ready", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestComponentPanicReturns500AndProducesAccessLog(t *testing.T) {
	accessLogs := make([]AccessLog, 0, 1)
	gateway, err := New(
		WithRouteResolver(RouteResolverFunc(func(*http.Request) (Route, bool, error) {
			panic("route resolver failed")
		})),
		WithAccessLogger(AccessLoggerFunc(func(_ context.Context, accessLog AccessLog) error {
			accessLogs = append(accessLogs, accessLog)
			return nil
		})),
		withTestUpstreams(map[string]http.Handler{"backend": http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})}),
	)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	gateway.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://gateway/public", nil))
	if response.Code != http.StatusInternalServerError || len(accessLogs) != 1 {
		t.Fatalf("status = %d, access logs = %d", response.Code, len(accessLogs))
	}
	if accessLogs[0].ErrorCode != "gateway_panic" {
		t.Fatalf("access log = %#v", accessLogs[0])
	}
}

func TestChunkedRequestAboveRouteLimitReturns413(t *testing.T) {
	route := publicRoute()
	route.MaxBodyBytes = 4
	gateway, err := New(
		WithRoutes(route),
		WithUpstreams(Upstream{Name: "backend", URL: "http://backend.internal"}),
		WithTransport(roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			_, readErr := io.ReadAll(r.Body)
			return nil, readErr
		})),
	)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "http://gateway/public", strings.NewReader("too large"))
	request.ContentLength = -1
	response := httptest.NewRecorder()
	gateway.ServeHTTP(response, request)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

type recordingSlogHandler struct {
	mutex   sync.Mutex
	records []slog.Record
	err     error
}

func (*recordingSlogHandler) Enabled(context.Context, slog.Level) bool { return true }

func (handler *recordingSlogHandler) Handle(_ context.Context, record slog.Record) error {
	handler.mutex.Lock()
	defer handler.mutex.Unlock()
	handler.records = append(handler.records, record.Clone())
	return handler.err
}

func (handler *recordingSlogHandler) WithAttrs([]slog.Attr) slog.Handler { return handler }

func (handler *recordingSlogHandler) WithGroup(string) slog.Handler { return handler }

func (handler *recordingSlogHandler) snapshot() []slog.Record {
	handler.mutex.Lock()
	defer handler.mutex.Unlock()
	return append([]slog.Record(nil), handler.records...)
}

func slogRecordAttributes(record slog.Record) map[string]any {
	attributes := make(map[string]any)
	record.Attrs(func(attribute slog.Attr) bool {
		attributes[attribute.Key] = attribute.Value.Any()
		return true
	})
	return attributes
}
