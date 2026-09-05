package logging

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"reflect"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	defaultMaxMessageBytes = 16 * 1024
	defaultMaxStringBytes  = 16 * 1024
	defaultMaxAttributes   = 64
	defaultMaxDepth        = 8
	redactedValue          = "[REDACTED]"
	unserializableValue    = "[UNSERIALIZABLE]"
	truncatedValue         = "[TRUNCATED]"
)

var builtinSensitiveKeys = []string{
	"apikey", "authorization", "clientsecret", "cookie", "credential",
	"jwt", "password", "privatekey", "secret", "session", "token",
	"accesstoken", "refreshtoken", "idtoken", "sessiontoken", "csrftoken",
	"xsrftoken", "setcookie",
}

// ContextAttrs 从请求上下文提取项目自己的稳定字段；回调的 panic 由项目负责。
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

// SanitizingHandler 清洗支持的字段，不拥有输出流、等级或项目扩展点的错误策略。
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
	normalized, keys, err := normalizeOptions(options)
	if err != nil {
		return nil, err
	}
	return &SanitizingHandler{next: next, options: normalized, sensitiveKeys: keys}, nil
}

// Enabled 完全沿用下游 Handler 的等级判断与 panic 行为。
func (handler *SanitizingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return handler.next.Enabled(ctx, level)
}

// Handle 只隔离字段编码失败；项目回调和下游 Handler 的 panic 正常传播。
func (handler *SanitizingHandler) Handle(ctx context.Context, record slog.Record) error {
	if !handler.Enabled(ctx, record.Level) {
		return nil
	}
	state := sanitizer{handler: handler, remaining: handler.options.MaxAttributes}
	var fields []*logField
	for _, scoped := range handler.attrs {
		if state.remaining == 0 {
			break
		}
		state.add(&fields, scoped.groups, scoped.attr, 1)
	}
	if state.remaining > 0 && handler.options.ContextAttrs != nil {
		for _, attr := range handler.options.ContextAttrs(ctx) {
			if state.remaining == 0 {
				break
			}
			state.add(&fields, handler.groups, attr, 1)
		}
	}
	record.Attrs(func(attr slog.Attr) bool {
		if state.remaining == 0 {
			return false
		}
		state.add(&fields, handler.groups, attr, 1)
		return state.remaining > 0
	})
	sanitized := slog.NewRecord(record.Time, record.Level, truncateUTF8(record.Message, handler.options.MaxMessageBytes), record.PC)
	sanitized.AddAttrs(renderFields(fields)...)
	return handler.next.Handle(ctx, sanitized)
}

// WithAttrs 返回独立的字段列表，不修改调用方 slice；嵌套值仍由调用方持有。
func (handler *SanitizingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	cloned := handler.clone()
	for _, attr := range attrs {
		cloned.attrs = append(cloned.attrs, scopedAttr{groups: handler.groups, attr: attr})
	}
	return cloned
}

// WithGroup 的路径在每条记录中与普通具名组共同参与脱敏、深度和预算检查。
func (handler *SanitizingHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return handler
	}
	cloned := handler.clone()
	cloned.groups = append(cloned.groups, name)
	return cloned
}

func (handler *SanitizingHandler) clone() *SanitizingHandler {
	cloned := *handler
	cloned.attrs = append([]scopedAttr(nil), handler.attrs...)
	cloned.groups = append([]string(nil), handler.groups...)
	return &cloned
}

// logField 只保留预算内的输出节点，使共享组路径只消耗一次预算。
type logField struct {
	key      string
	value    slog.Value
	group    bool
	children []*logField
}

type sanitizer struct {
	handler   *SanitizingHandler
	remaining int
}

func (state *sanitizer) consume() bool {
	if state.remaining == 0 {
		return false
	}
	state.remaining--
	return true
}

func (state *sanitizer) add(target *[]*logField, groups []string, attr slog.Attr, depth int) {
	if state.remaining == 0 || attr.Equal(slog.Attr{}) {
		return
	}
	if len(groups) > 0 {
		group := state.group(target, groups[0], depth)
		if group.value.Kind() == slog.KindGroup {
			state.add(&group.children, groups[1:], attr, depth+1)
		}
		return
	}
	// 敏感值不求值，避免 LogValuer 或嵌套字段在被丢弃前执行。
	if attr.Key != "" && (state.handler.sensitiveKey(attr.Key) || depth > state.handler.options.MaxDepth) {
		state.consume()
		*target = append(*target, &logField{key: attr.Key, value: state.placeholder(attr.Key, depth)})
		return
	}
	value := attr.Value.Resolve()
	if value.Kind() == slog.KindGroup {
		if len(value.Group()) == 0 {
			return
		}
		children := target
		childDepth := depth
		if attr.Key != "" {
			group := state.group(target, attr.Key, depth)
			children = &group.children
			childDepth++
		}
		for _, child := range value.Group() {
			if state.remaining == 0 {
				break
			}
			state.add(children, nil, child, childDepth)
		}
		return
	}
	state.consume()
	*target = append(*target, &logField{key: attr.Key, value: slog.AnyValue(state.clean(value.Any(), depth))})
}

func (state *sanitizer) placeholder(key string, depth int) slog.Value {
	if state.handler.sensitiveKey(key) {
		return slog.StringValue(redactedValue)
	}
	if depth > state.handler.options.MaxDepth {
		return slog.StringValue(truncatedValue)
	}
	return slog.GroupValue()
}

func (state *sanitizer) group(target *[]*logField, key string, depth int) *logField {
	for _, field := range *target {
		if field.group && field.key == key {
			return field
		}
	}
	state.consume()
	field := &logField{key: key, value: state.placeholder(key, depth), group: true}
	*target = append(*target, field)
	return field
}

func renderFields(fields []*logField) []slog.Attr {
	attrs := make([]slog.Attr, 0, len(fields))
	for _, field := range fields {
		value := field.value
		if field.group && value.Kind() == slog.KindGroup {
			value = slog.GroupValue(renderFields(field.children)...)
		}
		attrs = append(attrs, slog.Attr{Key: field.key, Value: value})
	}
	return attrs
}

func (state *sanitizer) clean(value any, depth int) any {
	if depth > state.handler.options.MaxDepth {
		return truncatedValue
	}
	switch typed := value.(type) {
	case nil:
		return nil
	case time.Time:
		return typed
	case time.Duration:
		return typed
	case error:
		return truncateUTF8(safeErrorText(typed), state.handler.options.MaxStringBytes)
	case slog.Value:
		return state.cleanSlog(typed, depth)
	case slog.LogValuer:
		return state.cleanSlog(slog.AnyValue(typed), depth)
	}
	// 只遍历数据类型，不调用任意业务对象的 MarshalJSON、String 或指针解引用。
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.String:
		return truncateUTF8(reflected.String(), state.handler.options.MaxStringBytes)
	case reflect.Bool:
		return reflected.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return reflected.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return reflected.Uint()
	case reflect.Float32, reflect.Float64:
		number := reflected.Float()
		if math.IsNaN(number) || math.IsInf(number, 0) {
			return unserializableValue
		}
		if reflected.Kind() == reflect.Float32 {
			return float32(number)
		}
		return number
	case reflect.Map:
		if reflected.Type().Key().Kind() != reflect.String {
			return unserializableValue
		}
		if reflected.IsNil() {
			return nil
		}
		result := make(map[string]any)
		iterator := reflected.MapRange()
		for state.remaining > 0 && iterator.Next() {
			state.consume()
			key := iterator.Key().String()
			if state.handler.sensitiveKey(key) {
				result[key] = redactedValue
			} else {
				result[key] = state.clean(iterator.Value().Interface(), depth+1)
			}
		}
		return result
	case reflect.Slice, reflect.Array:
		if reflected.Kind() == reflect.Slice && reflected.Type().Elem().Kind() == reflect.Uint8 {
			return fmt.Sprintf("<bytes:%d>", reflected.Len())
		}
		if reflected.Kind() == reflect.Slice && reflected.IsNil() {
			return nil
		}
		result := make([]any, 0, min(reflected.Len(), state.remaining))
		for index := 0; index < reflected.Len() && state.consume(); index++ {
			result = append(result, state.clean(reflected.Index(index).Interface(), depth+1))
		}
		return result
	default:
		return unserializableValue
	}
}

func (state *sanitizer) cleanSlog(value slog.Value, depth int) any {
	value = value.Resolve()
	if value.Kind() != slog.KindGroup {
		return state.clean(value.Any(), depth)
	}
	var fields []*logField
	for _, attr := range value.Group() {
		state.add(&fields, nil, attr, depth+1)
	}
	return fieldsObject(fields)
}

func fieldsObject(fields []*logField) map[string]any {
	result := make(map[string]any)
	for _, field := range fields {
		if field.group && field.value.Kind() == slog.KindGroup {
			result[field.key] = fieldsObject(field.children)
		} else {
			result[field.key] = field.value.Any()
		}
	}
	return result
}

func (handler *SanitizingHandler) sensitiveKey(key string) bool {
	normalized := normalizeKey(key)
	for _, candidate := range handler.sensitiveKeys {
		if normalized == candidate {
			return true
		}
	}
	return false
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

func truncateUTF8(value string, limit int) string {
	value = strings.ToValidUTF8(value, "\uFFFD")
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
