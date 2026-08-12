// Package envconfig 提供小型且无外部依赖的环境变量解析器。
package envconfig

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// StrictLoader 记录首个非法环境变量值，同时返回回退值以完成配置构造。
type StrictLoader struct {
	err error
}

// NewStrictLoader 创建一个拒绝显式非法值的环境变量加载器。
func NewStrictLoader() *StrictLoader {
	return &StrictLoader{}
}

// Err 返回首个严格解析错误。
func (loader *StrictLoader) Err() error {
	return loader.err
}

// Duration 解析必须为正数的时长，并记录显式非法值。
func (loader *StrictLoader) Duration(key string, fallback time.Duration) time.Duration {
	value, err := DurationStrict(key, fallback)
	loader.record(err)
	return value
}

// Int 解析整数，并记录显式非法值。
func (loader *StrictLoader) Int(key string, fallback int) int {
	value, err := IntStrict(key, fallback)
	loader.record(err)
	return value
}

// ByteSize 解析必须为正数的字节大小，并记录显式非法值。
func (loader *StrictLoader) ByteSize(key string, fallback int64) int64 {
	value, err := ByteSizeStrict(key, fallback)
	loader.record(err)
	return value
}

// Bool 解析布尔值，并记录显式非法值。
func (loader *StrictLoader) Bool(key string, fallback bool) bool {
	value, err := BoolStrict(key, fallback)
	loader.record(err)
	return value
}

// CSV 解析非空的逗号分隔列表，并记录显式非法值。
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

// String 返回去除首尾空白的环境变量值；未设置或为空时返回回退值。
func String(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

// RequiredString 返回去除首尾空白的环境变量值以及它是否存在。
func RequiredString(key string) (string, bool) {
	value := strings.TrimSpace(os.Getenv(key))
	return value, value != ""
}

// Duration 解析 Go 时长或以秒表示的正数。
func Duration(key string, fallback time.Duration) time.Duration {
	parsed, err := DurationStrict(key, fallback)
	if err == nil {
		return parsed
	}
	return fallback
}

// DurationStrict 解析 Go 时长或正整数秒，并拒绝显式非法值。
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

// Int 解析整数环境变量值，失败时返回回退值。
func Int(key string, fallback int) int {
	parsed, err := IntStrict(key, fallback)
	if err != nil {
		return fallback
	}
	return parsed
}

// IntStrict 解析整数，并拒绝显式非法值。
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

// Int64 解析正 int64 环境变量值，失败时返回回退值。
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

// ByteSize 解析正整数及可选的 B、KB、MB、GB、KiB、MiB 或 GiB 后缀。
func ByteSize(key string, fallback int64) int64 {
	parsed, err := ByteSizeStrict(key, fallback)
	if err != nil {
		return fallback
	}
	return parsed
}

// ByteSizeStrict 解析正数字节大小，并拒绝非法或溢出的显式值。
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

// Bool 解析常见布尔值，未设置时返回回退值。
func Bool(key string, fallback bool) bool {
	parsed, err := BoolStrict(key, fallback)
	if err != nil {
		return fallback
	}
	return parsed
}

// BoolStrict 解析常见布尔值，并拒绝显式非法值。
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

// CSV 解析逗号分隔的非空值。
func CSV(key, fallback string) []string {
	return parseCSV(String(key, fallback))
}

// CSVStrict 解析非空的逗号分隔列表，并拒绝显式空列表。
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

// CSVAllowEmpty 区分显式空值和未设置值。
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
