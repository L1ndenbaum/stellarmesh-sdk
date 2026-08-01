// Package console formats accepted events for container logs.
package console

import (
	"context"
	"fmt"
	"io"
	"strings"

	sharedlogging "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging"
)

// Sink writes a human-readable line for every accepted event.
type Sink struct {
	Writer io.Writer
	Color  bool
}

// WriteBatch formats a batch to the configured writer.
func (sink *Sink) WriteBatch(_ context.Context, events []sharedlogging.Event) error {
	for _, event := range events {
		if _, err := fmt.Fprintln(sink.Writer, Format(event, sink.Color)); err != nil {
			return err
		}
	}
	return nil
}

// Format builds one deterministic console line.
func Format(event sharedlogging.Event, color bool) string {
	level := string(event.Level)
	if color {
		level = colorize(event.Level, level)
	}
	parts := []string{event.Timestamp.UTC().Format("2006-01-02T15:04:05.000Z07:00"), level, event.Service, event.Message}
	if event.TraceID != "" {
		parts = append(parts, "trace_id="+event.TraceID)
	}
	return strings.Join(parts, " | ")
}

func colorize(level sharedlogging.Level, value string) string {
	code := "37"
	switch level {
	case sharedlogging.LevelDebug:
		code = "36"
	case sharedlogging.LevelInfo:
		code = "32"
	case sharedlogging.LevelWarning:
		code = "33"
	case sharedlogging.LevelError, sharedlogging.LevelAudit:
		code = "31"
	}
	return "\x1b[" + code + "m" + value + "\x1b[0m"
}
