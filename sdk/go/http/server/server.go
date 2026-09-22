// Package server 构造设置了边界的 net/http 服务。
package server

import (
	"net/http"
	"time"
)

// Config 包含共享 HTTP 服务超时配置。
type Config struct {
	// Addr 直接传给 net/http；空值使用其默认监听地址。
	Addr string
	// ReadHeaderTimeout 限制读取请求头的时长；零值沿用 net/http 的 ReadTimeout 规则。
	ReadHeaderTimeout time.Duration
	// ReadTimeout 限制整个请求读取；零值不设上限。
	ReadTimeout time.Duration
	// WriteTimeout 限制响应写入；零值不设上限，长流应由业务选择合适值。
	WriteTimeout time.Duration
	// IdleTimeout 限制 keep-alive 空闲时长；零值沿用 net/http 的 ReadTimeout 规则。
	IdleTimeout time.Duration
}

// New 使用 cfg 和 handler 构造 HTTP 服务，不监听端口或启动 goroutine。
// 调用方负责 ListenAndServe、处理 ErrServerClosed，并在退出时调用 Shutdown。
func New(cfg Config, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
	}
}
