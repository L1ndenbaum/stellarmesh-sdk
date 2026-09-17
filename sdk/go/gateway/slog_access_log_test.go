package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
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
	results, ok := attributes["rate_limit_result"].([]slog.Attr)
	if !ok || len(results) != 1 || !results[0].Equal(slog.String("client_ip", "allowed")) {
		t.Fatalf("rate_limit_result = %#v", attributes["rate_limit_result"])
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
