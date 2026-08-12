package api

import "net/http"

// TokenAuthConfig 控制共享 token 鉴权。
type TokenAuthConfig struct {
	Header          string
	Token           string
	Message         string
	AllowEmptyToken bool
}

// TokenAuth 在调用下一个 handler 前校验共享 token 请求头。
func TokenAuth(cfg TokenAuthConfig) func(http.Handler) http.Handler {
	message := cfg.Message
	if message == "" {
		message = "unauthorized"
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if cfg.Token == "" && cfg.AllowEmptyToken {
				next.ServeHTTP(w, r)
				return
			}
			if cfg.Token == "" || r.Header.Get(cfg.Header) != cfg.Token {
				WriteError(w, http.StatusUnauthorized, message)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
