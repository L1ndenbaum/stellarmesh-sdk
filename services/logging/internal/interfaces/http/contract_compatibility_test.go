package httpapi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	sharedlogging "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging"
)

func TestGoLoggingDecodersAcceptSharedContractFixtures(t *testing.T) {
	if _, err := sharedlogging.DecodeEvent(readLoggingContract(t, "testdata", "valid-event.json")); err != nil {
		t.Fatal(err)
	}
	deadLetter, err := sharedlogging.DecodeDeadLetter(readLoggingContract(t, "testdata", "valid-dead-letter.json"))
	if err != nil {
		t.Fatal(err)
	}
	if deadLetter.SourceOffset != 42 || deadLetter.Reason != "invalid_event" {
		t.Fatalf("dead letter = %#v", deadLetter)
	}
	oversize, err := sharedlogging.DecodeOversizeDeadLetter(readLoggingContract(t, "testdata", "valid-dead-letter-v2.json"))
	if err != nil {
		t.Fatal(err)
	}
	if oversize.SourceOffset != 43 || oversize.Reason != "source_message_too_large" || !oversize.ContentOmitted {
		t.Fatalf("oversized dead letter = %#v", oversize)
	}
}

func TestGoLoggingDecoderRejectsSharedInvalidFixtures(t *testing.T) {
	var fixtures []struct {
		Name    string          `json:"name"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(readLoggingContract(t, "testdata", "invalid-events.json"), &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			if _, err := sharedlogging.DecodeEvent(fixture.Payload); err == nil {
				t.Fatal("DecodeEvent() accepted invalid fixture")
			}
		})
	}
}

func TestGoDeadLetterDecodersRequireExplicitZeroValuedFields(t *testing.T) {
	for _, test := range []struct {
		name    string
		fixture string
		field   string
		decode  func([]byte) error
	}{
		{
			name: "v1", fixture: "valid-dead-letter.json", field: "source_offset",
			decode: func(payload []byte) error { _, err := sharedlogging.DecodeDeadLetter(payload); return err },
		},
		{
			name: "v2", fixture: "valid-dead-letter-v2.json", field: "payload_bytes",
			decode: func(payload []byte) error { _, err := sharedlogging.DecodeOversizeDeadLetter(payload); return err },
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var object map[string]any
			if err := json.Unmarshal(readLoggingContract(t, "testdata", test.fixture), &object); err != nil {
				t.Fatal(err)
			}
			delete(object, test.field)
			payload, err := json.Marshal(object)
			if err != nil {
				t.Fatal(err)
			}
			if err := test.decode(payload); err == nil {
				t.Fatalf("decoder accepted missing %s", test.field)
			}
		})
	}
}

func TestGoLoggingLimitsMatchSharedContract(t *testing.T) {
	var limits struct {
		SchemaVersion         string `json:"schema_version"`
		MaxEventJSONBytes     int    `json:"max_event_json_bytes"`
		MaxHTTPBodyBytes      int    `json:"max_http_body_bytes"`
		MaxKafkaKeyValueBytes int    `json:"max_kafka_key_value_bytes"`
		MaxKafkaMessageBytes  int    `json:"max_kafka_message_bytes"`
	}
	if err := json.Unmarshal(readLoggingContract(t, "limits.json"), &limits); err != nil {
		t.Fatal(err)
	}
	if limits.SchemaVersion != "v1" || limits.MaxEventJSONBytes != sharedlogging.MaxEventJSONBytesV1 ||
		limits.MaxHTTPBodyBytes != sharedlogging.MaxHTTPBodyBytesV1 ||
		limits.MaxKafkaKeyValueBytes != sharedlogging.MaxKafkaKeyValueBytesV1 ||
		limits.MaxKafkaMessageBytes != sharedlogging.MaxKafkaMessageBytesV1 {
		t.Fatalf("contract limits do not match Go constants: %#v", limits)
	}
}

func readLoggingContract(t *testing.T, parts ...string) []byte {
	t.Helper()
	pathParts := append([]string{"..", "..", "..", "..", "..", "contracts", "logging", "v1"}, parts...)
	payload, err := os.ReadFile(filepath.Join(pathParts...))
	if err != nil {
		t.Fatal(err)
	}
	return payload
}
