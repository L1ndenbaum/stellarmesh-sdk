package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"reflect"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	defaultMaxMessageBytes = 16 * 1024
	defaultMaxStringBytes  = 16 * 1024
	defaultMaxAttributes   = 64
	defaultMaxDepth        = 8

	redactedValue       = "[REDACTED]"
	unserializableValue = "[UNSERIALIZABLE]"
	truncatedValue      = "[TRUNCATED]"
)

var (
	// ErrContextAttrsPanic 表示项目提供的上下文字段回调发生 panic。
	ErrContextAttrsPanic = errors.New("logging ContextAttrs callback panicked")
	// ErrHandlerPanic 表示被装饰的 slog Handler 发生 panic。
	ErrHandlerPanic = errors.New("decorated slog Handler panicked")
)

var builtinSensitiveKeys = []string{
	"apikey",
	"authorization",
	"clientsecret",
	"cookie",
	"credential",
	"jwt",
	"password",
	"privatekey",
	"secret",
	"session",
	"token",
}

// ContextAttrs 从请求上下文提取项目自己的稳定字段。
type ContextAttrs func(context.Context) []slog.Attr

// HandlerOptions 配置结构化字段的安全边界。
type HandlerOptions struct {
	ExtraSensitiveKeys []string
	MaxMessageBytes    int
	MaxStringBytes     int
	MaxAttributes      int
	MaxDepth           int
	ContextAttrs       ContextAttrs
}

type scopedAttr struct {
	groups []string
	attr   slog.Attr
}

// SanitizingHandler 在记录交给下游 Handler 前完成脱敏和有界化。
type SanitizingHandler struct {
	next          slog.Handler
	options       HandlerOptions
	sensitiveKeys []string
	attrs         []scopedAttr
	groups        []string
}

// NewSanitizingHandler 创建不拥有输出流和日志等级的 slog Handler 装饰器。
func NewSanitizingHandler(next slog.Handler, options HandlerOptions) (slog.Handler, error) {
	if isNil(next) {
		return nil, errors.New("logging slog Handler is required")
	}
	normalized, sensitiveKeys, err := normalizeOptions(options)
	if err != nil {
		return nil, err
	}
	return &SanitizingHandler{
		next:          next,
		options:       normalized,
		sensitiveKeys: sensitiveKeys,
	}, nil
}

// Enabled 完全沿用下游 Handler 的等级判断。
func (handler *SanitizingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return handler.next.Enabled(ctx, level)
}

// Handle 清洗当前记录并把下游 panic 转换为错误。
func (handler *SanitizingHandler) Handle(ctx context.Context, record slog.Record) (err error) {
	defer func() {
		if recover() != nil {
			err = ErrHandlerPanic
		}
	}()
	if !handler.next.Enabled(ctx, record.Level) {
		return nil
	}

	message := truncateUTF8(record.Message, handler.options.MaxMessageBytes)
	sanitized := slog.NewRecord(record.Time, record.Level, message, record.PC)
	remaining := handler.options.MaxAttributes
	cleanedAttrs := make([]slog.Attr, 0, remaining)

	for _, scoped := range handler.attrs {
		if remaining == 0 {
			break
		}
		if attr, used, ok := handler.sanitizeScopedAttr(scoped, handler.options.MaxDepth, remaining); ok {
			appendMergedAttr(&cleanedAttrs, attr)
			remaining -= used
		}
	}

	if remaining > 0 && handler.options.ContextAttrs != nil {
		contextAttrs, contextErr := callContextAttrs(handler.options.ContextAttrs, ctx)
		if contextErr != nil {
			return contextErr
		}
		for _, attr := range contextAttrs {
			if remaining == 0 {
				break
			}
			scoped := scopedAttr{groups: append([]string(nil), handler.groups...), attr: attr}
			if cleaned, used, ok := handler.sanitizeScopedAttr(scoped, handler.options.MaxDepth, remaining); ok {
				appendMergedAttr(&cleanedAttrs, cleaned)
				remaining -= used
			}
		}
	}

	if remaining > 0 {
		record.Attrs(func(attr slog.Attr) bool {
			if remaining == 0 {
				return false
			}
			scoped := scopedAttr{groups: append([]string(nil), handler.groups...), attr: attr}
			if cleaned, used, ok := handler.sanitizeScopedAttr(scoped, handler.options.MaxDepth, remaining); ok {
				appendMergedAttr(&cleanedAttrs, cleaned)
				remaining -= used
			}
			return remaining > 0
		})
	}
	sanitized.AddAttrs(cleanedAttrs...)

	return handler.next.Handle(ctx, sanitized)
}

// WithAttrs 返回携带独立固定字段的装饰器，不修改调用方 slice。
func (handler *SanitizingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	cloned := handler.clone()
	groups := append([]string(nil), handler.groups...)
	for _, attr := range attrs {
		cloned.attrs = append(cloned.attrs, scopedAttr{groups: groups, attr: attr})
	}
	return cloned
}

// WithGroup 返回把后续字段放入指定组的装饰器。
func (handler *SanitizingHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return handler
	}
	cloned := handler.clone()
	cloned.groups = append(cloned.groups, name)
	return cloned
}

func (handler *SanitizingHandler) clone() *SanitizingHandler {
	return &SanitizingHandler{
		next:          handler.next,
		options:       handler.options,
		sensitiveKeys: append([]string(nil), handler.sensitiveKeys...),
		attrs:         append([]scopedAttr(nil), handler.attrs...),
		groups:        append([]string(nil), handler.groups...),
	}
}

func normalizeOptions(options HandlerOptions) (HandlerOptions, []string, error) {
	if options.MaxMessageBytes == 0 {
		options.MaxMessageBytes = defaultMaxMessageBytes
	}
	if options.MaxStringBytes == 0 {
		options.MaxStringBytes = defaultMaxStringBytes
	}
	if options.MaxAttributes == 0 {
		options.MaxAttributes = defaultMaxAttributes
	}
	if options.MaxDepth == 0 {
		options.MaxDepth = defaultMaxDepth
	}
	if options.MaxMessageBytes < len(truncatedValue) || options.MaxStringBytes < len(truncatedValue) {
		return HandlerOptions{}, nil, errors.New("logging byte limits must fit the truncation marker")
	}
	if options.MaxAttributes < 1 {
		return HandlerOptions{}, nil, errors.New("logging MaxAttributes must be positive")
	}
	if options.MaxDepth < 1 {
		return HandlerOptions{}, nil, errors.New("logging MaxDepth must be positive")
	}

	sensitiveKeys := append([]string(nil), builtinSensitiveKeys...)
	for _, key := range options.ExtraSensitiveKeys {
		normalized := normalizeKey(key)
		if normalized == "" {
			return HandlerOptions{}, nil, errors.New("logging sensitive key must contain a letter or digit")
		}
		sensitiveKeys = append(sensitiveKeys, normalized)
	}
	options.ExtraSensitiveKeys = append([]string(nil), options.ExtraSensitiveKeys...)
	return options, sensitiveKeys, nil
}

func (handler *SanitizingHandler) sanitizeScopedAttr(scoped scopedAttr, maxDepth, remaining int) (slog.Attr, int, bool) {
	attr, used, ok := handler.sanitizeAttr(scoped.attr, 1, maxDepth, remaining)
	if !ok {
		return slog.Attr{}, 0, false
	}
	for index := len(scoped.groups) - 1; index >= 0; index-- {
		if scoped.groups[index] == "" {
			continue
		}
		attr = slog.Attr{Key: scoped.groups[index], Value: slog.GroupValue(attr)}
	}
	return attr, used, true
}

func appendMergedAttr(target *[]slog.Attr, attr slog.Attr) {
	if attr.Value.Kind() != slog.KindGroup {
		*target = append(*target, attr)
		return
	}
	for index := range *target {
		existing := &(*target)[index]
		if existing.Key != attr.Key || existing.Value.Kind() != slog.KindGroup {
			continue
		}
		children := append([]slog.Attr(nil), existing.Value.Group()...)
		for _, child := range attr.Value.Group() {
			appendMergedAttr(&children, child)
		}
		existing.Value = slog.GroupValue(children...)
		return
	}
	*target = append(*target, attr)
}

func (handler *SanitizingHandler) sanitizeAttr(attr slog.Attr, depth, maxDepth, remaining int) (slog.Attr, int, bool) {
	if remaining < 1 || attr.Key == "" && attr.Value.Kind() != slog.KindGroup {
		return slog.Attr{}, 0, false
	}
	if handler.sensitiveKey(attr.Key) {
		return slog.String(attr.Key, redactedValue), 1, true
	}
	if depth > maxDepth {
		return slog.String(attr.Key, truncatedValue), 1, true
	}

	value := attr.Value.Resolve()
	if value.Kind() != slog.KindGroup {
		return slog.Any(attr.Key, handler.sanitizeValue(slogValue(value), depth, maxDepth)), 1, true
	}

	children := make([]slog.Attr, 0, len(value.Group()))
	used := 0
	for _, child := range value.Group() {
		if used >= remaining {
			break
		}
		cleaned, childUsed, ok := handler.sanitizeAttr(child, depth+1, maxDepth, remaining-used)
		if ok {
			children = append(children, cleaned)
			used += childUsed
		}
	}
	if len(children) == 0 {
		return slog.Attr{}, 0, false
	}
	return slog.Attr{Key: attr.Key, Value: slog.GroupValue(children...)}, used, true
}

func (handler *SanitizingHandler) sanitizeValue(value any, depth, maxDepth int) any {
	if depth > maxDepth {
		return truncatedValue
	}
	switch typed := value.(type) {
	case error:
		return truncateUTF8(safeErrorText(typed), handler.options.MaxStringBytes)
	case map[string]any:
		result := make(map[string]any, len(typed))
		count := 0
		for key, nested := range typed {
			if count >= handler.options.MaxAttributes {
				break
			}
			if handler.sensitiveKey(key) {
				result[key] = redactedValue
			} else {
				result[key] = handler.sanitizeValue(nested, depth+1, maxDepth)
			}
			count++
		}
		return result
	case []any:
		limit := len(typed)
		if limit > handler.options.MaxAttributes {
			limit = handler.options.MaxAttributes
		}
		result := make([]any, 0, limit)
		for _, nested := range typed[:limit] {
			result = append(result, handler.sanitizeValue(nested, depth+1, maxDepth))
		}
		return result
	case string:
		return truncateUTF8(typed, handler.options.MaxStringBytes)
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
		return handler.sanitizeValue(normalized, depth, maxDepth)
	}
}

func (handler *SanitizingHandler) sensitiveKey(key string) bool {
	normalized := normalizeKey(key)
	for _, candidate := range handler.sensitiveKeys {
		if strings.Contains(normalized, candidate) {
			return true
		}
	}
	return false
}

func callContextAttrs(callback ContextAttrs, ctx context.Context) (attrs []slog.Attr, err error) {
	defer func() {
		if recover() != nil {
			err = ErrContextAttrsPanic
		}
	}()
	return callback(ctx), nil
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

func slogValue(value slog.Value) any {
	switch value.Kind() {
	case slog.KindBool:
		return value.Bool()
	case slog.KindDuration:
		return value.Duration()
	case slog.KindFloat64:
		return value.Float64()
	case slog.KindInt64:
		return value.Int64()
	case slog.KindString:
		return value.String()
	case slog.KindTime:
		return value.Time()
	case slog.KindUint64:
		return value.Uint64()
	default:
		return value.Any()
	}
}

func truncateUTF8(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	prefixLimit := limit - len(truncatedValue)
	for prefixLimit > 0 && !utf8.ValidString(value[:prefixLimit]) {
		prefixLimit--
	}
	return value[:prefixLimit] + truncatedValue
}

func normalizeKey(key string) string {
	return strings.Map(func(value rune) rune {
		if unicode.IsLetter(value) || unicode.IsDigit(value) {
			return unicode.ToLower(value)
		}
		return -1
	}, key)
}

func safeErrorText(value error) (result string) {
	defer func() {
		if recover() != nil {
			result = unserializableValue
		}
	}()
	return value.Error()
}

func isNil(value any) bool {
	if value == nil {
		return true
	}
	kind := reflect.ValueOf(value).Kind()
	return (kind == reflect.Chan || kind == reflect.Func || kind == reflect.Interface || kind == reflect.Map || kind == reflect.Pointer || kind == reflect.Slice) && reflect.ValueOf(value).IsNil()
}
