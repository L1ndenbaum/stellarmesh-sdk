package observability

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestLoggerFormatsShareFilteringAndRedaction(t *testing.T) {
	for _, format := range []string{"", "pretty", "json"} {
		t.Run(format, func(t *testing.T) {
			var output bytes.Buffer
			logger, err := NewLogger(&output, "warning", format)
			if err != nil {
				t.Fatal(err)
			}
			logger.Info("filtered")
			logger.Warn("written", "password", "hidden", "count", 2)
			slog.NewLogLogger(logger.Handler(), slog.LevelError).Print("http-failure")
			text := output.String()
			if strings.Contains(text, "hidden") || strings.Contains(text, "filtered") ||
				!strings.Contains(text, "[REDACTED]") || strings.Count(text, "written") != 1 ||
				!strings.Contains(text, "storage-service") || !strings.Contains(text, "ERROR") {
				t.Fatalf("unexpected output: %s", text)
			}
			if format == "json" {
				for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
					var event map[string]any
					if err := json.Unmarshal([]byte(line), &event); err != nil {
						t.Fatal(err)
					}
				}
			} else if strings.HasPrefix(text, "{") {
				t.Fatalf("pretty should be text: %s", text)
			}
		})
	}
}

func TestInvalidLogConfigurationFailsWithoutEchoingInput(t *testing.T) {
	for _, setting := range [][2]string{{"secret-level", "json"}, {"info", "secret-format"}} {
		var output bytes.Buffer
		logger, err := NewLogger(&output, setting[0], setting[1])
		if err == nil || strings.Contains(err.Error(), "secret-") {
			t.Fatalf("unexpected error: %v", err)
		}
		logger.Error("startup failed", "error", err)
		if !strings.Contains(output.String(), "ERROR") {
			t.Fatal("startup failure was lost")
		}
	}
}
