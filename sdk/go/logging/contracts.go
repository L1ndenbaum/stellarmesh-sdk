// Package logging contains the canonical logging v1 contract and clients.
package logging

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// TopicV1 is the canonical Kafka topic name for logging v1 events.
const TopicV1 = "stellarmesh.logging.events.v1"

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

// IngestResult reports how many events entered the ingester queue.
type IngestResult struct {
	Accepted int `json:"accepted"`
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
