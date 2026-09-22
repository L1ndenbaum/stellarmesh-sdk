package gateway

import (
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// CORSConfig 控制网关拥有的浏览器跨域策略。
type CORSConfig struct {
	// AllowedOrigins 启用 CORS 时必填；通配符不能和凭证或其他 origin 混用。
	AllowedOrigins []string
	// AllowedMethods 留空允许 DELETE、GET、HEAD、OPTIONS、PATCH、POST、PUT。
	AllowedMethods []string
	// AllowedHeaders 留空允许 Authorization、Content-Type、X-Request-ID。
	AllowedHeaders []string
	// AllowCredentials 默认 false；为 true 时必须使用明确的来源列表。
	AllowCredentials bool
	// MaxAge 为预检缓存时长；0 不输出缓存时间，负数拒绝。
	MaxAge time.Duration
}

var defaultCORSMethods = []string{"DELETE", "GET", "HEAD", "OPTIONS", "PATCH", "POST", "PUT"}

var defaultCORSHeaders = []string{"Authorization", "Content-Type", "X-Request-ID"}

type corsPolicy struct {
	origins          map[string]struct{}
	wildcard         bool
	methods          map[string]struct{}
	headers          map[string]string
	allowOriginValue string
	methodsValue     string
	headersValue     string
	credentials      bool
	maxAge           time.Duration
}

// WithCORS 启用显式的浏览器跨域策略。
func WithCORS(cors CORSConfig) Option {
	return componentOption("cors", func(config *config) error {
		copied := cors
		copied.AllowedOrigins = append([]string(nil), cors.AllowedOrigins...)
		copied.AllowedMethods = append([]string(nil), cors.AllowedMethods...)
		copied.AllowedHeaders = append([]string(nil), cors.AllowedHeaders...)
		config.cors = &copied
		return nil
	})
}

func newCORSPolicy(config *CORSConfig) (*corsPolicy, error) {
	if config == nil {
		return nil, nil
	}
	if len(config.AllowedOrigins) == 0 {
		return nil, errors.New("gateway CORS allowed origins are required")
	}
	policy := &corsPolicy{
		origins:     make(map[string]struct{}, len(config.AllowedOrigins)),
		methods:     make(map[string]struct{}),
		headers:     make(map[string]string),
		credentials: config.AllowCredentials,
		maxAge:      config.MaxAge,
	}
	for _, origin := range config.AllowedOrigins {
		origin = strings.TrimSpace(origin)
		if origin == "" {
			return nil, errors.New("gateway CORS origin cannot be empty")
		}
		if origin == "*" {
			policy.wildcard = true
		} else if !validCORSOrigin(origin) {
			return nil, errors.New("gateway CORS origin is invalid: " + origin)
		}
		policy.origins[origin] = struct{}{}
	}
	if policy.wildcard && (len(policy.origins) != 1 || policy.credentials) {
		return nil, errors.New("gateway CORS wildcard origin cannot be combined with credentials or explicit origins")
	}
	methods := config.AllowedMethods
	if len(methods) == 0 {
		methods = defaultCORSMethods
	}
	for _, method := range methods {
		method = strings.ToUpper(strings.TrimSpace(method))
		if !isHTTPToken(method) {
			return nil, errors.New("gateway CORS method is invalid")
		}
		policy.methods[method] = struct{}{}
	}
	headers := config.AllowedHeaders
	if len(headers) == 0 {
		headers = defaultCORSHeaders
	}
	for _, header := range headers {
		header = http.CanonicalHeaderKey(strings.TrimSpace(header))
		if !isHTTPToken(header) {
			return nil, errors.New("gateway CORS header is invalid")
		}
		policy.headers[strings.ToLower(header)] = header
	}
	if config.MaxAge < 0 {
		return nil, errors.New("gateway CORS max age cannot be negative")
	}
	policy.methodsValue = strings.Join(sortedMapKeys(policy.methods), ",")
	policy.headersValue = strings.Join(sortedMapValues(policy.headers), ",")
	if policy.wildcard {
		policy.allowOriginValue = "*"
	}
	return policy, nil
}

func validCORSOrigin(origin string) bool {
	parsed, err := url.Parse(origin)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != "" &&
		parsed.User == nil && parsed.Path == "" && parsed.RawQuery == "" && parsed.Fragment == ""
}

// handle 返回 true 表示 CORS 已经完成或拒绝该请求。
func (policy *corsPolicy) handle(w http.ResponseWriter, r *http.Request, gateway *Gateway) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return false
	}
	if !policy.wildcard {
		if _, allowed := policy.origins[origin]; !allowed {
			gateway.fail(w, r, GatewayError{Status: http.StatusForbidden, Code: "cors_origin_rejected", Message: "forbidden"})
			return true
		}
	}
	requestedMethod := r.Method
	preflight := r.Method == http.MethodOptions && strings.TrimSpace(r.Header.Get("Access-Control-Request-Method")) != ""
	if preflight {
		requestedMethod = strings.ToUpper(strings.TrimSpace(r.Header.Get("Access-Control-Request-Method")))
	}
	if _, allowed := policy.methods[requestedMethod]; !allowed {
		gateway.fail(w, r, GatewayError{Status: http.StatusForbidden, Code: "cors_method_rejected", Message: "forbidden"})
		return true
	}
	if preflight && !policy.acceptsRequestedHeaders(r.Header.Get("Access-Control-Request-Headers")) {
		gateway.fail(w, r, GatewayError{Status: http.StatusForbidden, Code: "cors_headers_rejected", Message: "forbidden"})
		return true
	}
	header := w.Header()
	header.Add("Vary", "Origin")
	if preflight {
		header.Add("Vary", "Access-Control-Request-Method")
		header.Add("Vary", "Access-Control-Request-Headers")
	}
	if policy.allowOriginValue != "" {
		header.Set("Access-Control-Allow-Origin", policy.allowOriginValue)
	} else {
		header.Set("Access-Control-Allow-Origin", origin)
	}
	header.Set("Access-Control-Allow-Methods", policy.methodsValue)
	header.Set("Access-Control-Allow-Headers", policy.headersValue)
	if policy.credentials {
		header.Set("Access-Control-Allow-Credentials", "true")
	}
	if policy.maxAge > 0 {
		header.Set("Access-Control-Max-Age", strconv.FormatInt(int64(policy.maxAge/time.Second), 10))
	}
	if preflight {
		w.WriteHeader(http.StatusNoContent)
		return true
	}
	return false
}

func (policy *corsPolicy) acceptsRequestedHeaders(raw string) bool {
	if strings.TrimSpace(raw) == "" {
		return true
	}
	for _, header := range strings.Split(raw, ",") {
		if _, allowed := policy.headers[strings.ToLower(strings.TrimSpace(header))]; !allowed {
			return false
		}
	}
	return true
}

func sortedMapKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedMapValues(values map[string]string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
