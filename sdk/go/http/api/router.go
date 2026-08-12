package api

import "github.com/go-chi/chi/v5"

// Router 是服务入口使用的 Chi 路由类型。
type Router = chi.Mux

// NewRouter 返回空路由。
func NewRouter() *Router {
	return chi.NewRouter()
}
