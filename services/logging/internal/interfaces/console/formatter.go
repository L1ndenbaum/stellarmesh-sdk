// Package console 为容器日志格式化已接受事件。
package console

import (
	"context"
	"fmt"
	"io"
	"strings"

	sharedlogging "github.com/L1ndenbaum/stellarmesh-sdk/sdk/go/logging"
)

// Sink 为每个已接受事件写入一行可读文本。
type Sink struct {
	Writer io.Writer
	Color  bool
}

// WriteBatch 将批次格式化到配置的 writer。
func (sink *Sink) WriteBatch(_ context.Context, events []sharedlogging.Event) error {
	for _, event := range events {
		if _, err := fmt.Fprintln(sink.Writer, Format(event, sink.Color)); err != nil {
			return err
		}
	}
	return nil
}

// Format 构造一行确定性的控制台文本。
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
