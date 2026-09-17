package gateway

import (
	"errors"
	"net/http"
	"strings"
)

// ErrInvalidCredential 表示凭证格式无效；不包含凭证内容。
var ErrInvalidCredential = errors.New("invalid credential")

// CredentialExtractor 提取凭证；空字符串表示缺失，其他错误表示组件故障。
// 格式错误应返回或包装 ErrInvalidCredential。
type CredentialExtractor func(*http.Request) (string, error)

// BearerCredential 提取唯一的 Authorization Bearer 凭证。
func BearerCredential() CredentialExtractor {
	return func(r *http.Request) (string, error) {
		values := r.Header.Values("Authorization")
		if len(values) == 0 {
			return "", nil
		}
		if len(values) != 1 {
			return "", ErrInvalidCredential
		}
		value := strings.TrimSpace(values[0])
		scheme, token, ok := strings.Cut(value, " ")
		if !ok || !strings.EqualFold(scheme, "Bearer") {
			return "", ErrInvalidCredential
		}
		token = strings.TrimLeft(token, " ")
		if token == "" {
			return "", ErrInvalidCredential
		}
		padding := false
		for _, c := range token {
			if c == '=' {
				padding = true
				continue
			}
			if padding || !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-._~+/", c)) {
				return "", ErrInvalidCredential
			}
		}
		if token[0] == '=' {
			return "", ErrInvalidCredential
		}
		return token, nil
	}
}

// CookieCredential 提取指定 Cookie；拒绝格式错误和重复同名 Cookie，避免歧义。
func CookieCredential(name string) (CredentialExtractor, error) {
	if !isHTTPToken(name) {
		return nil, errors.New("credential cookie name is invalid")
	}
	return func(r *http.Request) (string, error) {
		value, found := "", false
		for _, line := range r.Header.Values("Cookie") {
			// Request.Cookies 会跳过损坏片段；认证入口必须显式拒绝，不能静默降级。
			cookies, err := http.ParseCookie(line)
			if err != nil {
				return "", ErrInvalidCredential
			}
			for _, cookie := range cookies {
				if cookie.Name != name {
					continue
				}
				if found {
					return "", ErrInvalidCredential
				}
				found, value = true, cookie.Value
			}
		}
		return value, nil
	}, nil
}
