// Package logging contains the canonical logging v1 contract and clients.
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
	// TopicV1 is the canonical Kafka topic name for logging v1 events.
	TopicV1 = "stellarmesh.logging.events.v1"
	// DeadLetterTopicV1 is the canonical Kafka topic name for rejected v1 payloads.
	DeadLetterTopicV1 = "stellarmesh.logging.events.v1.dlq"
	// DeadLetterSchemaV1 identifies the dead-letter record layout.
	DeadLetterSchemaV1 = "v1"
	// DeadLetterSchemaV2 identifies the compact oversized-message dead-letter layout.
	DeadLetterSchemaV2 = "v2"
	// MaxEventJSONBytesV1 is the maximum compact JSON size of one canonical event.
	MaxEventJSONBytesV1 = 900 * 1024
	// MaxHTTPBodyBytesV1 is the maximum accepted ingestion request body size.
	MaxHTTPBodyBytesV1 = 1 << 20
	// MaxKafkaKeyValueBytesV1 reserves protocol overhead below the Kafka message limit.
	MaxKafkaKeyValueBytesV1 = 960 * 1024
	// MaxKafkaMessageBytesV1 is the maximum serialized Kafka record size.
	MaxKafkaMessageBytesV1 = 1 << 20
)

const maxDeadLetterErrorRunes = 2048

// Level is a severity value accepted by the logging contract.
type Level string

const (
	LevelDebug   Level = "DEBUG"
	LevelInfo    Level = "INFO"
	LevelWarning Level = "WARNING"
	LevelError   Level = "ERROR"
	LevelAudit   Level = "AUDIT"
)

var validLevels = map[Level]struct{}{
	LevelDebug: {}, LevelInfo: {}, LevelWarning: {}, LevelError: {}, LevelAudit: {},
}

// Event is the canonical logging v1 record.
type Event struct {
	EventID   string         `json:"event_id"`
	Timestamp time.Time      `json:"timestamp"`
	Level     Level          `json:"level"`
	Service   string         `json:"service"`
	Message   string         `json:"message"`
	TraceID   string         `json:"trace_id"`
	Metadata  map[string]any `json:"metadata"`
}

// IngestRequest wraps one event for the v1 HTTP endpoint.
type IngestRequest struct {
	Event Event `json:"event"`
}

// BatchIngestRequest wraps multiple events for the v1 batch endpoint.
type BatchIngestRequest struct {
	Events []Event `json:"events"`
}

// IngestResult reports how many events were durably accepted by Kafka or the local spool.
type IngestResult struct {
	Accepted int `json:"accepted"`
}

// DeadLetter preserves one rejected Kafka message and its source coordinates.
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

// OversizeDeadLetter records a compact digest for a Kafka message that cannot be copied into DLQ v1.
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

// KafkaPartitionKeyV1 returns a bounded stable key while preserving trace co-partitioning.
func KafkaPartitionKeyV1(event Event) []byte {
	if event.TraceID == "" {
		return []byte(event.EventID)
	}
	digest := sha256.Sum256([]byte(event.TraceID))
	return digest[:]
}

// FitsKafkaKeyValueBudgetV1 reports whether a compact event can be safely wrapped as a Kafka record.
func FitsKafkaKeyValueBudgetV1(event Event, payloadBytes int) bool {
	return payloadBytes >= 0 && payloadBytes+len(KafkaPartitionKeyV1(event)) <= MaxKafkaKeyValueBytesV1
}

// Validate verifies a severity value.
func (level Level) Validate() error {
	if _, ok := validLevels[level]; !ok {
		return fmt.Errorf("invalid log level %q", level)
	}
	return nil
}

// Validate verifies every field required by the v1 contract.
func (event Event) Validate() error {
	if !validEventID(event.EventID) {
		return errors.New("event_id must be a canonical UUID")
	}
	if event.Timestamp.IsZero() {
		return errors.New("timestamp is required")
	}
	if err := event.Level.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(event.Service) == "" {
		return errors.New("service is required")
	}
	if strings.TrimSpace(event.Message) == "" {
		return errors.New("message is required")
	}
	if event.Metadata == nil {
		return errors.New("metadata is required")
	}
	return nil
}

// Validate verifies every field required by the v1 dead-letter contract.
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

// Validate verifies every field required by the v2 oversized-message dead-letter contract.
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

// NewEventID creates a random RFC 4122 version 4 UUID.
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

// DecodeEvent strictly decodes one canonical event and rejects unknown fields.
func DecodeEvent(payload []byte) (Event, error) {
	if err := requireJSONFields(payload, "event", "event_id", "timestamp", "level", "service", "message", "trace_id", "metadata"); err != nil {
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

// DecodeDeadLetter strictly decodes one v1 dead-letter record.
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

// DecodeOversizeDeadLetter strictly decodes one v2 oversized-message dead-letter record.
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
