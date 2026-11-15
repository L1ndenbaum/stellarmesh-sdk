package gateway

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

// TimeoutProfile 声明可复用的上游超时。名称可由静态路由或动态路由引用。
type TimeoutProfile struct {
	Name string
	// ResponseHeaderTimeout 从请求体发送完成计时，零值沿用原 Transport。
	ResponseHeaderTimeout time.Duration
	// ResponseBodyIdleTimeout 限定单次等待上游响应体数据的时间；零值关闭。
	ResponseBodyIdleTimeout time.Duration
}

// WithTimeoutProfiles 在启动时准备有限的连接池。设置响应头超时需要 *http.Transport。
func WithTimeoutProfiles(profiles ...TimeoutProfile) Option {
	return componentOption("timeout_profiles", func(c *config) error {
		if len(profiles) == 0 {
			return errors.New("gateway timeout profiles are empty")
		}
		c.timeoutProfiles = append([]TimeoutProfile(nil), profiles...)
		return nil
	})
}

func profileTransports(base http.RoundTripper, profiles []TimeoutProfile) (map[string]http.RoundTripper, error) {
	transports := map[string]http.RoundTripper{"": base}
	pools := map[time.Duration]http.RoundTripper{0: base}
	if tr, ok := base.(*http.Transport); ok {
		pools[tr.ResponseHeaderTimeout] = base
	}
	for _, p := range profiles {
		name := strings.TrimSpace(p.Name)
		if _, exists := transports[name]; exists || p.ResponseHeaderTimeout < 0 || p.ResponseBodyIdleTimeout < 0 {
			return nil, errors.New("invalid or duplicate gateway timeout profile: " + name)
		}
		tr := pools[p.ResponseHeaderTimeout]
		if tr == nil {
			standard, ok := base.(*http.Transport)
			if !ok {
				return nil, errors.New("response header timeout requires an HTTP transport")
			}
			pool := standard.Clone()
			pool.ResponseHeaderTimeout = p.ResponseHeaderTimeout
			pools[p.ResponseHeaderTimeout] = pool
			tr = pool
		}
		if p.ResponseBodyIdleTimeout > 0 {
			tr = idleTransport{base: tr, timeout: p.ResponseBodyIdleTimeout}
		}
		transports[name] = tr
	}
	return transports, nil
}

type idleTransport struct {
	base    http.RoundTripper
	timeout time.Duration
}

func (t idleTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	// 升级后的双向连接由代理隧道持有，必须保留其 ReadWriteCloser。
	if r.Header.Get("Upgrade") != "" {
		return t.base.RoundTrip(r)
	}
	ctx, cancel := context.WithCancel(r.Context())
	resp, err := t.base.RoundTrip(r.WithContext(ctx))
	if err != nil {
		cancel()
		return nil, err
	}
	if resp.Body == nil || resp.Body == http.NoBody {
		cancel()
		return resp, nil
	}
	resp.Body = &idleResponseBody{ReadCloser: resp.Body, timeout: t.timeout, cancel: cancel}
	return resp, nil
}

type idleResponseBody struct {
	io.ReadCloser
	timeout time.Duration
	cancel  context.CancelFunc
}

func (b *idleResponseBody) Read(data []byte) (int, error) {
	// 仅在等待上游数据时计时，下游写阻塞或整个流的持续时间不占用读取预算。
	timer := time.AfterFunc(b.timeout, b.cancel)
	n, err := b.ReadCloser.Read(data)
	if !timer.Stop() {
		return n, context.DeadlineExceeded
	}
	return n, err
}

func (b *idleResponseBody) Close() error {
	b.cancel()
	return b.ReadCloser.Close()
}
