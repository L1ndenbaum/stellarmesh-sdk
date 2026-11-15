package gateway

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httputil"
)

var errResponseHook = errors.New("gateway upstream response hook failed")

// ResponseHook 根据入站请求、最终上游状态码与响应头副本，返回要应用的响应头策略。
// 入站请求供只读查询 Cookie、路径参数和认证上下文；钩子不读取请求体或响应体。
// 出错或 panic 时丢弃上游最终响应，由 ErrorResponder 返回 503。
type ResponseHook func(request *http.Request, status int, header http.Header) (HeaderPolicy, error)

func withResponseHook(proxy *httputil.ReverseProxy, upstreamHook ResponseHook, requestIDHeader string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hook := upstreamHook
		if route, ok := r.Context().Value(routeContextKey).(Route); ok && route.ResponseHook != nil {
			hook = route.ResponseHook
		}
		if hook == nil {
			proxy.ServeHTTP(w, r)
			return
		}

		// 回调捕获本次入站请求，仍可读取出站策略移除的 Cookie。共享代理和连接池不持有请求状态。
		scoped := *proxy
		scoped.ModifyResponse = func(resp *http.Response) error {
			if err := applyResponseHook(hook, r, resp, requestIDHeader); err != nil {
				return err
			}
			return proxy.ModifyResponse(resp)
		}
		scoped.ServeHTTP(w, r)
	})
}

func applyResponseHook(hook ResponseHook, r *http.Request, resp *http.Response, id string) (err error) {
	defer func() {
		if recover() != nil {
			err = errResponseHook
		}
	}()
	policy, err := hook(r, resp.StatusCode, resp.Header.Clone())
	if err == nil {
		policy, err = compileHeaderPolicy(policy, id, false)
	}
	if err != nil {
		return fmt.Errorf("%w: %w", errResponseHook, err)
	}
	policy.apply(resp.Header)
	return nil
}
