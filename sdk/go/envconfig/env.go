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
