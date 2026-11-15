package gateway

import (
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

// HeaderPolicy 在固定上游的请求或响应上设置、追加或删除字段。一个字段只能声明一种操作。
type HeaderPolicy struct {
	Set    http.Header
	Add    http.Header
	Remove []string
}

func compileHeaderPolicy(input HeaderPolicy, id string, request bool) (HeaderPolicy, error) {
	result := HeaderPolicy{}
	seen := make(map[string]bool)
	register := func(key string, values []string, setting bool) (string, error) {
		key = http.CanonicalHeaderKey(key)
		if !isHTTPToken(key) || seen[key] || reservedProxyHeader(key) || strings.EqualFold(key, id) {
			return "", fmt.Errorf("invalid, reserved or duplicate gateway proxy header: %q", key)
		}
		seen[key] = true
		if request && isForwardingHeader(key) {
			// 只允许配置中单一的固定来源覆盖外部协议、域名和端口，客户端地址始终由网关生成。
			if !setting || len(values) != 1 || !validForwardedValue(key, values[0]) {
				return "", fmt.Errorf("invalid gateway forwarding header: %s", key)
			}
		}
		for _, value := range values {
			for i := 0; i < len(value); i++ {
				c := value[i]
				if c == 0x7f || c < 0x20 && c != '\t' {
					return "", fmt.Errorf("invalid value for gateway header: %s", key)
				}
			}
		}
		return key, nil
	}
	for _, key := range input.Remove {
		name, err := register(key, nil, false)
		if err != nil {
			return result, err
		}
		result.Remove = append(result.Remove, name)
	}
	compile := func(headers http.Header, setting bool) (http.Header, error) {
		if len(headers) == 0 {
			return nil, nil
		}
		canonical := make(http.Header, len(headers))
		for k, values := range headers {
			key, err := register(k, values, setting)
			if err != nil {
				return nil, err
			}
			if len(values) == 0 {
				return nil, fmt.Errorf("missing values for gateway header: %s", key)
			}
			canonical[key] = append([]string(nil), values...)
		}
		return canonical, nil
	}
	var err error
	result.Set, err = compile(input.Set, true)
	if err != nil {
		return result, err
	}
	result.Add, err = compile(input.Add, false)
	return result, err
}

func reservedProxyHeader(key string) bool {
	switch key {
	case "Host", "Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade", "Content-Length", "X-User-Id", "X-User-Roles":
		return true
	}
	return false
}

func validForwardedValue(key, value string) bool {
	switch key {
	case "X-Forwarded-Host":
		return validAuthority(value)
	case "X-Forwarded-Proto":
		return value == "http" || value == "https"
	case "X-Forwarded-Port":
		return validPort(value)
	}
	return false
}

func validPort(port string) bool {
	value, err := strconv.ParseUint(port, 10, 16)
	return err == nil && value > 0
}

func validAuthority(authority string) bool {
	u, err := url.Parse("http://" + authority)
	if err != nil || u.Host != authority || u.Hostname() == "" || strings.HasSuffix(authority, ":") {
		return false
	}
	if strings.ContainsAny(authority, "[]") || strings.Contains(u.Hostname(), ":") {
		if !strings.HasPrefix(authority, "[") {
			return false
		}
		if _, err := netip.ParseAddr(u.Hostname()); err != nil {
			return false
		}
	}
	return u.Port() == "" || validPort(u.Port())
}

func (p HeaderPolicy) apply(header http.Header) {
	for _, key := range p.Remove {
		header.Del(key)
	}
	for key, values := range p.Set {
		header[key] = append([]string(nil), values...)
	}
	for key, values := range p.Add {
		header[key] = append(header[key], values...)
	}
}
