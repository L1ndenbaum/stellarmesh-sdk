// Package envconfig provides small, dependency-free environment parsers.
package envconfig

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// StrictLoader collects the first invalid environment value while returning fallbacks for construction.
type StrictLoader struct {
	err error
}

// NewStrictLoader creates an environment loader that rejects explicitly invalid values.
func NewStrictLoader() *StrictLoader {
	return &StrictLoader{}
}

// Err returns the first strict parsing failure.
func (loader *StrictLoader) Err() error {
	return loader.err
}

// Duration parses a required-positive duration and records invalid explicit values.
func (loader *StrictLoader) Duration(key string, fallback time.Duration) time.Duration {
	value, err := DurationStrict(key, fallback)
	loader.record(err)
	return value
}

// Int parses an integer and records invalid explicit values.
func (loader *StrictLoader) Int(key string, fallback int) int {
	value, err := IntStrict(key, fallback)
	loader.record(err)
	return value
}

// ByteSize parses a required-positive byte size and records invalid explicit values.
func (loader *StrictLoader) ByteSize(key string, fallback int64) int64 {
	value, err := ByteSizeStrict(key, fallback)
	loader.record(err)
	return value
}

// Bool parses a boolean and records invalid explicit values.
func (loader *StrictLoader) Bool(key string, fallback bool) bool {
	value, err := BoolStrict(key, fallback)
	loader.record(err)
	return value
}

// CSV parses a non-empty comma-separated list and records invalid explicit values.
func (loader *StrictLoader) CSV(key, fallback string) []string {
	value, err := CSVStrict(key, fallback)
	loader.record(err)
	return value
}

func (loader *StrictLoader) record(err error) {
	if loader.err == nil && err != nil {
		loader.err = err
	}
}

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
	parsed, err := DurationStrict(key, fallback)
	if err == nil {
		return parsed
	}
	return fallback
}

// DurationStrict parses a Go duration or positive integer seconds and rejects invalid explicit values.
func DurationStrict(key string, fallback time.Duration) (time.Duration, error) {
	value, exists := os.LookupEnv(key)
	value = strings.TrimSpace(value)
	if !exists || value == "" {
		return fallback, nil
	}
	if parsed, err := time.ParseDuration(value); err == nil && parsed > 0 {
		return parsed, nil
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds > 0 && seconds <= int64(^uint64(0)>>1)/int64(time.Second) {
		return time.Duration(seconds) * time.Second, nil
	}
	return fallback, fmt.Errorf("%s must be a positive duration or integer seconds", key)
}

// Int parses an integer environment value or returns fallback.
func Int(key string, fallback int) int {
	parsed, err := IntStrict(key, fallback)
	if err != nil {
		return fallback
	}
	return parsed
}

// IntStrict parses an integer and rejects invalid explicit values.
func IntStrict(key string, fallback int) (int, error) {
	value, exists := os.LookupEnv(key)
	value = strings.TrimSpace(value)
	if !exists || value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	return parsed, nil
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
	parsed, err := ByteSizeStrict(key, fallback)
	if err != nil {
		return fallback
	}
	return parsed
}

// ByteSizeStrict parses a positive byte size and rejects invalid or overflowing explicit values.
func ByteSizeStrict(key string, fallback int64) (int64, error) {
	value, exists := os.LookupEnv(key)
	value = strings.TrimSpace(value)
	if !exists || value == "" {
		return fallback, nil
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
		return fallback, fmt.Errorf("%s must be a positive, non-overflowing byte size", key)
	}
	return parsed * multiplier, nil
}

// Bool parses common truthy values or returns fallback when unset.
func Bool(key string, fallback bool) bool {
	parsed, err := BoolStrict(key, fallback)
	if err != nil {
		return fallback
	}
	return parsed
}

// BoolStrict parses common boolean values and rejects invalid explicit values.
func BoolStrict(key string, fallback bool) (bool, error) {
	value, exists := os.LookupEnv(key)
	value = strings.TrimSpace(strings.ToLower(value))
	if !exists || value == "" {
		return fallback, nil
	}
	switch value {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	default:
		return fallback, fmt.Errorf("%s must be a boolean", key)
	}
}

// CSV parses comma-separated non-empty values.
func CSV(key, fallback string) []string {
	return parseCSV(String(key, fallback))
}

// CSVStrict parses a non-empty comma-separated list and rejects an explicitly empty list.
func CSVStrict(key, fallback string) ([]string, error) {
	raw, exists := os.LookupEnv(key)
	if !exists {
		raw = fallback
	}
	values := parseCSV(raw)
	if exists && len(values) == 0 {
		return parseCSV(fallback), fmt.Errorf("%s must contain at least one value", key)
	}
	return values, nil
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
