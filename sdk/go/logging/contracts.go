// Package logging 包含规范日志 v2 契约和客户端。
package logging

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

const (
	// TopicV2 是日志 v2 事件的规范 Kafka topic 名称。
	TopicV2 = "stellarmesh.logging.events.v2"
	// DeadLetterTopicV2 是被拒绝 v2 载荷的规范 Kafka topic 名称。
	DeadLetterTopicV2 = "stellarmesh.logging.events.v2.dlq"
	// DeadLetterSchemaV1 标识死信记录结构。
	DeadLetterSchemaV1 = "v1"
	// DeadLetterSchemaV2 标识超大消息的紧凑死信记录结构。
	DeadLetterSchemaV2 = "v2"
	// MaxEventJSONBytesV2 是单个规范事件紧凑 JSON 的大小上限。
	MaxEventJSONBytesV2 = 900 * 1024
	// MaxHTTPBodyBytesV2 是接收请求体的大小上限。
	MaxHTTPBodyBytesV2 = 1 << 20
	// MaxKafkaKeyValueBytesV2 在 Kafka 消息上限内为协议开销保留空间。
	MaxKafkaKeyValueBytesV2 = 960 * 1024
	// MaxKafkaMessageBytesV2 是序列化 Kafka 记录的大小上限。
	MaxKafkaMessageBytesV2 = 1 << 20
)

const maxDeadLetterErrorRunes = 2048

// EventKind 描述事件用途，不参与严重程度排序。
type EventKind string

const (
	EventKindLog   EventKind = "LOG"
	EventKindAudit EventKind = "AUDIT"
)

var validEventKinds = map[EventKind]struct{}{
	EventKindLog: {}, EventKindAudit: {},
}

// Level 是日志契约接受的严重级别。
type Level string

const (
	LevelDebug   Level = "DEBUG"
	LevelInfo    Level = "INFO"
	LevelWarning Level = "WARNING"
	LevelError   Level = "ERROR"
)

var validLevels = map[Level]struct{}{
	LevelDebug: {}, LevelInfo: {}, LevelWarning: {}, LevelError: {},
}

// Event 是规范日志 v2 记录。
type Event struct {
	EventID   string         `json:"event_id"`
	Timestamp time.Time      `json:"timestamp"`
	Kind      EventKind      `json:"kind"`
	Level     Level          `json:"level"`
	Service   string         `json:"service"`
	Message   string         `json:"message"`
	TraceID   string         `json:"trace_id"`
	Metadata  map[string]any `json:"metadata"`
}

// IngestRequest 为 v2 HTTP 端点封装一个事件。
type IngestRequest struct {
	Event Event `json:"event"`
}

// BatchIngestRequest 为 v2 批量端点封装多个事件。
type BatchIngestRequest struct {
	Events []Event `json:"events"`
}

// IngestResult 报告由 Kafka 或本地 spool 持久接收的事件数量。
type IngestResult struct {
	Accepted int `json:"accepted"`
}

// DeadLetter 保存一条被拒绝的 Kafka 消息及其来源坐标。
type DeadLetter struct {
	SchemaVersion   string     `json:"schema_version"`
	SourceTopic     string     `json:"source_topic"`
	SourcePartition int        `json:"source_partition"`
	SourceOffset    int64      `json:"source_offset"`
	SourceTimestamp *time.Time `json:"source_timestamp,omitempty"`
	SourceKeyBase64 string     `json:"source_key_base64"`
	Reason          string     `json:"reason"`
	Error           string     `json:"error"`
	PayloadBase64   string     `json:"payload_base64"`
	FailedAt        time.Time  `json:"failed_at"`
}

// OversizeDeadLetter 记录无法复制到 DLQ v1 的 Kafka 消息紧凑摘要。
type OversizeDeadLetter struct {
	SchemaVersion   string     `json:"schema_version"`
	SourceTopic     string     `json:"source_topic"`
	SourcePartition int        `json:"source_partition"`
	SourceOffset    int64      `json:"source_offset"`
	SourceTimestamp *time.Time `json:"source_timestamp,omitempty"`
	Reason          string     `json:"reason"`
	Error           string     `json:"error"`
	SourceKeyBytes  int64      `json:"source_key_bytes"`
	SourceKeySHA256 string     `json:"source_key_sha256"`
	PayloadBytes    int64      `json:"payload_bytes"`
	PayloadSHA256   string     `json:"payload_sha256"`
	ContentOmitted  bool       `json:"content_omitted"`
	FailedAt        time.Time  `json:"failed_at"`
}

// KafkaPartitionKeyV2 返回有界稳定键，同时保持同一 trace 位于相同分区。
func KafkaPartitionKeyV2(event Event) []byte {
	if event.TraceID == "" {
		return []byte(event.EventID)
	}
	digest := sha256.Sum256([]byte(event.TraceID))
	return digest[:]
}

// FitsKafkaKeyValueBudgetV2 判断紧凑事件能否安全封装为 Kafka 记录。
func FitsKafkaKeyValueBudgetV2(event Event, payloadBytes int) bool {
	return payloadBytes >= 0 && payloadBytes+len(KafkaPartitionKeyV2(event)) <= MaxKafkaKeyValueBytesV2
}

// Validate 校验事件种类。
func (kind EventKind) Validate() error {
	if _, ok := validEventKinds[kind]; !ok {
		return fmt.Errorf("invalid log event kind %q", kind)
	}
	return nil
}

// Validate 校验严重级别。
func (level Level) Validate() error {
	if _, ok := validLevels[level]; !ok {
		return fmt.Errorf("invalid log level %q", level)
	}
	return nil
}

// Validate 校验 v2 契约要求的所有字段。
func (event Event) Validate() error {
	if !validEventID(event.EventID) {
		return errors.New("event_id must be a canonical UUID")
	}
	if event.Timestamp.IsZero() {
		return errors.New("timestamp is required")
	}
	if err := event.Kind.Validate(); err != nil {
		return err
	}
	if err := event.Level.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(event.Service) == "" || event.Service != strings.TrimSpace(event.Service) {
		return errors.New("service must be non-empty and trimmed")
	}
	if strings.TrimSpace(event.Message) == "" {
		return errors.New("message is required")
	}
	if event.Metadata == nil {
		return errors.New("metadata is required")
	}
	return nil
}

// Validate 校验 v1 死信契约要求的所有字段。
func (deadLetter DeadLetter) Validate() error {
	if deadLetter.SchemaVersion != DeadLetterSchemaV1 {
		return fmt.Errorf("unsupported dead-letter schema version %q", deadLetter.SchemaVersion)
	}
	if strings.TrimSpace(deadLetter.SourceTopic) == "" {
		return errors.New("dead-letter source_topic is required")
	}
	if deadLetter.SourcePartition < 0 || deadLetter.SourceOffset < 0 {
		return errors.New("dead-letter source coordinates must not be negative")
	}
	if deadLetter.SourceTimestamp != nil && deadLetter.SourceTimestamp.IsZero() {
		return errors.New("dead-letter source_timestamp must not be zero")
	}
	if deadLetter.Reason != "invalid_event" {
		return fmt.Errorf("unsupported dead-letter reason %q", deadLetter.Reason)
	}
	if strings.TrimSpace(deadLetter.Error) == "" || len([]rune(deadLetter.Error)) > maxDeadLetterErrorRunes {
		return errors.New("dead-letter error must contain 1 to 2048 characters")
	}
	if _, err := decodeBase64(deadLetter.SourceKeyBase64); err != nil {
		return fmt.Errorf("dead-letter source_key_base64: %w", err)
	}
	if _, err := decodeBase64(deadLetter.PayloadBase64); err != nil {
		return fmt.Errorf("dead-letter payload_base64: %w", err)
	}
	if deadLetter.FailedAt.IsZero() {
		return errors.New("dead-letter failed_at is required")
	}
	return nil
}

// Validate 校验 v2 超大消息死信契约要求的所有字段。
func (deadLetter OversizeDeadLetter) Validate() error {
	if deadLetter.SchemaVersion != DeadLetterSchemaV2 {
		return fmt.Errorf("unsupported oversized dead-letter schema version %q", deadLetter.SchemaVersion)
	}
	if strings.TrimSpace(deadLetter.SourceTopic) == "" {
		return errors.New("oversized dead-letter source_topic is required")
	}
	if deadLetter.SourcePartition < 0 || deadLetter.SourceOffset < 0 {
		return errors.New("oversized dead-letter source coordinates must not be negative")
	}
	if deadLetter.SourceTimestamp != nil && deadLetter.SourceTimestamp.IsZero() {
		return errors.New("oversized dead-letter source_timestamp must not be zero")
	}
	if deadLetter.Reason != "source_message_too_large" {
		return fmt.Errorf("unsupported oversized dead-letter reason %q", deadLetter.Reason)
	}
	if strings.TrimSpace(deadLetter.Error) == "" || len([]rune(deadLetter.Error)) > maxDeadLetterErrorRunes {
		return errors.New("oversized dead-letter error must contain 1 to 2048 characters")
	}
	if deadLetter.SourceKeyBytes < 0 || deadLetter.PayloadBytes < 0 {
		return errors.New("oversized dead-letter byte sizes must not be negative")
	}
	if !validSHA256(deadLetter.SourceKeySHA256) || !validSHA256(deadLetter.PayloadSHA256) {
		return errors.New("oversized dead-letter hashes must be lowercase SHA-256 values")
	}
	if !deadLetter.ContentOmitted {
		return errors.New("oversized dead-letter content_omitted must be true")
	}
	if deadLetter.FailedAt.IsZero() {
		return errors.New("oversized dead-letter failed_at is required")
	}
	return nil
}

// NewEventID 创建随机的 RFC 4122 版本 4 UUID。
func NewEventID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(raw)
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32], nil
}

// DecodeEvent 严格解码一个规范事件并拒绝未知字段。
func DecodeEvent(payload []byte) (Event, error) {
	if err := requireJSONFields(payload, "event", "event_id", "timestamp", "kind", "level", "service", "message", "trace_id", "metadata"); err != nil {
		return Event{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var event Event
	if err := decoder.Decode(&event); err != nil {
		return Event{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return Event{}, errors.New("event payload must contain one JSON value")
		}
		return Event{}, err
	}
	if err := event.Validate(); err != nil {
		return Event{}, err
	}
	return event, nil
}

// DecodeDeadLetter 严格解码一条 v1 死信记录。
func DecodeDeadLetter(payload []byte) (DeadLetter, error) {
	if err := requireJSONFields(
		payload, "dead-letter", "schema_version", "source_topic", "source_partition", "source_offset",
		"source_key_base64", "reason", "error", "payload_base64", "failed_at",
	); err != nil {
		return DeadLetter{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var deadLetter DeadLetter
	if err := decoder.Decode(&deadLetter); err != nil {
		return DeadLetter{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return DeadLetter{}, errors.New("dead-letter payload must contain one JSON value")
		}
		return DeadLetter{}, err
	}
	if err := deadLetter.Validate(); err != nil {
		return DeadLetter{}, err
	}
	return deadLetter, nil
}

// DecodeOversizeDeadLetter 严格解码一条 v2 超大消息死信记录。
func DecodeOversizeDeadLetter(payload []byte) (OversizeDeadLetter, error) {
	if err := requireJSONFields(
		payload, "oversize dead-letter", "schema_version", "source_topic", "source_partition", "source_offset",
		"reason", "error", "source_key_bytes", "source_key_sha256", "payload_bytes", "payload_sha256",
		"content_omitted", "failed_at",
	); err != nil {
		return OversizeDeadLetter{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var deadLetter OversizeDeadLetter
	if err := decoder.Decode(&deadLetter); err != nil {
		return OversizeDeadLetter{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return OversizeDeadLetter{}, errors.New("oversized dead-letter payload must contain one JSON value")
		}
		return OversizeDeadLetter{}, err
	}
	if err := deadLetter.Validate(); err != nil {
		return OversizeDeadLetter{}, err
	}
	return deadLetter, nil
}

func requireJSONFields(payload []byte, kind string, required ...string) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		return err
	}
	for _, field := range required {
		if _, ok := fields[field]; !ok {
			return fmt.Errorf("%s field %q is required", kind, field)
		}
	}
	return nil
}

func decodeBase64(value string) ([]byte, error) {
	return base64.StdEncoding.Strict().DecodeString(value)
}

func validEventID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	if strings.ToLower(value) != value {
		return false
	}
	decoded := strings.ReplaceAll(value, "-", "")
	_, err := hex.DecodeString(decoded)
	return err == nil
}

func validSHA256(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
