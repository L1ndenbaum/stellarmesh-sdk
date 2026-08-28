package logging

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"unicode"
)

const (
	maxSanitizeDepth    = 6
	maxStringLength     = 2048
	maxSequenceLength   = 50
	redactedValue       = "[REDACTED]"
	maxDepthValue       = "[MAX_DEPTH]"
	unserializableValue = "[UNSERIALIZABLE]"
	truncatedValueLabel = "...[TRUNCATED]"
)

var sensitiveKeyParts = []string{
	"api_key", "authorization", "cookie", "credential", "jwt", "password", "secret", "token",
}

// SanitizeMetadata 移除常见 Secret，并限制嵌套元数据。
func SanitizeMetadata(metadata map[string]any) map[string]any {
	if metadata == nil {
		return map[string]any{}
	}
	sanitized := make(map[string]any, len(metadata))
	for key, value := range metadata {
		if sensitiveKey(key) {
			sanitized[key] = redactedValue
			continue
		}
		sanitized[key] = sanitizeValue(value, 1)
	}
	return sanitized
}

func sanitizeValue(value any, depth int) any {
	if depth > maxSanitizeDepth {
		return maxDepthValue
	}
	switch typed := value.(type) {
	case error:
		return sanitizeValue(typed.Error(), depth)
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, nested := range typed {
			if sensitiveKey(key) {
				result[key] = redactedValue
			} else {
				result[key] = sanitizeValue(nested, depth+1)
			}
		}
		return result
	case []any:
		limit := len(typed)
		if limit > maxSequenceLength {
			limit = maxSequenceLength
		}
		result := make([]any, 0, limit+1)
		for _, nested := range typed[:limit] {
			result = append(result, sanitizeValue(nested, depth+1))
		}
		if len(typed) > limit {
			result = append(result, fmt.Sprintf("...[%d more]", len(typed)-limit))
		}
		return result
	case string:
		runes := []rune(typed)
		if len(runes) > maxStringLength {
			return string(runes[:maxStringLength]) + truncatedValueLabel
		}
		return typed
	case []byte:
		return fmt.Sprintf("<bytes:%d>", len(typed))
	case float32:
		if math.IsNaN(float64(typed)) || math.IsInf(float64(typed), 0) {
			return unserializableValue
		}
		return typed
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			return unserializableValue
		}
		return typed
	case json.Number:
		return typed
	case nil, bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return typed
	default:
		normalized, err := normalizeJSONValue(typed)
		if err != nil {
			return unserializableValue
		}
		return sanitizeValue(normalized, depth)
	}
}

func normalizeJSONValue(value any) (any, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var normalized any
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	if err := decoder.Decode(&normalized); err != nil {
		return nil, err
	}
	return normalized, nil
}

func sensitiveKey(key string) bool {
	normalized := strings.Map(func(value rune) rune {
		if unicode.IsLetter(value) || unicode.IsDigit(value) {
			return unicode.ToLower(value)
		}
		return -1
	}, key)
	for _, part := range sensitiveKeyParts {
		canonicalPart := strings.ReplaceAll(part, "_", "")
		if strings.Contains(normalized, canonicalPart) {
			return true
		}
	}
	return false
}
