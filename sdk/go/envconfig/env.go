// Package envconfig provides small, dependency-free environment parsers.
package envconfig

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// String returns a trimmed environment value or fallback when it is unset or empty.
func String(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

// RequiredString returns a trimmed environment value and whether it was present.
func RequiredString(key string) (string, bool) {
	value := strings.TrimSpace(os.Getenv(key))
	return value, value != ""
}

// Duration parses a Go duration or a positive number of seconds.
func Duration(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	if parsed, err := time.ParseDuration(value); err == nil && parsed > 0 {
		return parsed
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	return fallback
}

// Int parses an integer environment value or returns fallback.
func Int(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

// Int64 parses a positive int64 environment value or returns fallback.
func Int64(key string, fallback int64) int64 {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

// ByteSize parses a positive integer with an optional B, KB, MB, GB, KiB, MiB, or GiB suffix.
func ByteSize(key string, fallback int64) int64 {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	upper := strings.ToUpper(value)
	multipliers := []struct {
		suffix     string
		multiplier int64
	}{
		{suffix: "GIB", multiplier: 1 << 30},
		{suffix: "MIB", multiplier: 1 << 20},
		{suffix: "KIB", multiplier: 1 << 10},
		{suffix: "GB", multiplier: 1_000_000_000},
		{suffix: "MB", multiplier: 1_000_000},
		{suffix: "KB", multiplier: 1_000},
		{suffix: "B", multiplier: 1},
	}
	multiplier := int64(1)
	number := upper
	for _, candidate := range multipliers {
		if strings.HasSuffix(upper, candidate.suffix) {
			multiplier = candidate.multiplier
			number = strings.TrimSpace(strings.TrimSuffix(upper, candidate.suffix))
			break
		}
	}
	parsed, err := strconv.ParseInt(number, 10, 64)
	if err != nil || parsed <= 0 || parsed > (1<<63-1)/multiplier {
		return fallback
	}
	return parsed * multiplier
}

// Bool parses common truthy values or returns fallback when unset.
func Bool(key string, fallback bool) bool {
	value := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	if value == "" {
		return fallback
	}
	return value == "1" || value == "true" || value == "yes" || value == "on"
}

// CSV parses comma-separated non-empty values.
func CSV(key, fallback string) []string {
	return parseCSV(String(key, fallback))
}

// CSVAllowEmpty distinguishes an explicitly empty value from an unset value.
func CSVAllowEmpty(key, fallback string) []string {
	raw, exists := os.LookupEnv(key)
	if !exists {
		raw = fallback
	}
	return parseCSV(raw)
}

func parseCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			values = append(values, value)
		}
	}
	return values
}
