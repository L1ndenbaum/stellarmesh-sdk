package console

import (
	"strings"
	"testing"
	"time"

	sharedlogging "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging"
)

func TestFormatIncludesCoreFields(t *testing.T) {
	line := Format(sharedlogging.Event{
		Timestamp: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC), Level: sharedlogging.LevelInfo,
		Service: "backend", Message: "created", TraceID: "trace-1",
	}, false)
	for _, value := range []string{"INFO", "backend", "created", "trace_id=trace-1"} {
		if !strings.Contains(line, value) {
			t.Fatalf("line = %q", line)
		}
	}
}
