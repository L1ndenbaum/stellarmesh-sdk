package gateway

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// AccessMode 描述路由是否需要认证身份。
type AccessMode uint8

const (
	// AccessProtected 是路由的安全默认值，要求请求通过认证。
	AccessProtected AccessMode = iota
	// AccessPublic 显式声明不需要认证的公开路由。
	AccessPublic
)

// RouteMatch 描述一个静态路由的匹配条件。
type RouteMatch struct {
	// Methods 留空匹配任意方法；显式方法会规范化为大写。
	Methods []string
	// ExactPath 与 PathPrefix 必须且只能设置一个。
	ExactPath string
	// PathPrefix 按字符串前缀匹配，优先选择最长前缀；需要路径段边界时请显式包含斜杠。
	PathPrefix string
}

// Route 描述请求的稳定路由结果。
type Route struct {
	Name     string
	Match    RouteMatch
	Upstream string
	// Access 零值为 AccessProtected；公开路由必须显式使用 AccessPublic。
	Access AccessMode
	// MaxBodyBytes 为请求体字节上限；0 不设限制，负数拒绝。
	MaxBodyBytes int64
}

// RouteResolver 把请求解析为一个项目声明的路由。
type RouteResolver interface {
	Resolve(*http.Request) (Route, bool, error)
}

// RouteResolverFunc 让函数直接实现 RouteResolver。
type RouteResolverFunc func(*http.Request) (Route, bool, error)

type staticRouteResolver struct {
	exact    []compiledRoute
	prefixes []compiledRoute
}

type compiledRoute struct {
	route   Route
	methods map[string]struct{}
}

// Resolve 调用路由解析函数。
func (resolver RouteResolverFunc) Resolve(r *http.Request) (Route, bool, error) {
	return resolver(r)
}

// WithRoutes 使用经过启动校验的静态路由表。
func WithRoutes(routes ...Route) Option {
	return componentOption("routes", func(config *config) error {
		config.staticRoutes = append([]Route(nil), routes...)
		return nil
	})
}

// WithRouteResolver 使用项目提供的动态路由解析器。
func WithRouteResolver(resolver RouteResolver) Option {
	return componentOption("route_resolver", func(config *config) error {
		if isNilInterface(resolver) {
			return errors.New("gateway route resolver is nil")
		}
		config.routeResolver = resolver
		return nil
	})
}

func newStaticRouteResolver(routes []Route) (*staticRouteResolver, error) {
	if len(routes) == 0 {
		return nil, errors.New("gateway routes are required")
	}
	resolver := &staticRouteResolver{}
	seen := make(map[string]struct{}, len(routes))
	for index, route := range routes {
		compiled, err := compileRoute(route)
		if err != nil {
			return nil, errors.New("invalid gateway route at index " + strconv.Itoa(index) + ": " + err.Error())
		}
		key := routeMatchKey(compiled)
		if _, exists := seen[key]; exists {
			return nil, errors.New("duplicate gateway route match: " + route.Name)
		}
		seen[key] = struct{}{}
		if compiled.route.Match.ExactPath != "" {
			resolver.exact = append(resolver.exact, compiled)
		} else {
			resolver.prefixes = append(resolver.prefixes, compiled)
		}
	}
	sort.SliceStable(resolver.prefixes, func(left, right int) bool {
		return len(resolver.prefixes[left].route.Match.PathPrefix) > len(resolver.prefixes[right].route.Match.PathPrefix)
	})
	return resolver, nil
}

func compileRoute(route Route) (compiledRoute, error) {
	route.Name = strings.TrimSpace(route.Name)
	route.Upstream = strings.TrimSpace(route.Upstream)
	route.Match.ExactPath = strings.TrimSpace(route.Match.ExactPath)
	route.Match.PathPrefix = strings.TrimSpace(route.Match.PathPrefix)
	if route.Name == "" {
		return compiledRoute{}, errors.New("route name is required")
	}
	if route.Upstream == "" {
		return compiledRoute{}, errors.New("route upstream is required")
	}
	if (route.Match.ExactPath == "") == (route.Match.PathPrefix == "") {
		return compiledRoute{}, errors.New("exact path or path prefix must be set exclusively")
	}
	path := route.Match.ExactPath
	if path == "" {
		path = route.Match.PathPrefix
	}
	if !strings.HasPrefix(path, "/") {
		return compiledRoute{}, errors.New("route path must start with slash")
	}
	if route.Access != AccessProtected && route.Access != AccessPublic {
		return compiledRoute{}, errors.New("route access mode is invalid")
	}
	if route.MaxBodyBytes < 0 {
		return compiledRoute{}, errors.New("maximum request body bytes cannot be negative")
	}
	methods := make(map[string]struct{}, len(route.Match.Methods))
	for _, method := range route.Match.Methods {
		method = strings.ToUpper(strings.TrimSpace(method))
		if !isHTTPToken(method) {
			return compiledRoute{}, errors.New("route method is invalid")
		}
		methods[method] = struct{}{}
	}
	route.Match.Methods = sortedKeys(methods)
	return compiledRoute{route: route, methods: methods}, nil
}

func normalizeResolvedRoute(route Route) (Route, error) {
	route = cloneRoute(route)
	route.Name = strings.TrimSpace(route.Name)
	route.Upstream = strings.TrimSpace(route.Upstream)
	if route.Name == "" || route.Upstream == "" {
		return Route{}, errors.New("resolved route name and upstream are required")
	}
	if route.Access != AccessProtected && route.Access != AccessPublic {
		return Route{}, errors.New("resolved route access mode is invalid")
	}
	if route.MaxBodyBytes < 0 {
		return Route{}, errors.New("resolved route maximum request body bytes cannot be negative")
	}
	return route, nil
}

func (resolver *staticRouteResolver) Resolve(r *http.Request) (Route, bool, error) {
	for _, route := range resolver.exact {
		if route.route.Match.ExactPath == r.URL.Path && route.accepts(r.Method) {
			return cloneRoute(route.route), true, nil
		}
	}
	for _, route := range resolver.prefixes {
		if strings.HasPrefix(r.URL.Path, route.route.Match.PathPrefix) && route.accepts(r.Method) {
			return cloneRoute(route.route), true, nil
		}
	}
	return Route{}, false, nil
}

func (route compiledRoute) accepts(method string) bool {
	if len(route.methods) == 0 {
		return true
	}
	_, ok := route.methods[strings.ToUpper(method)]
	return ok
}

func routeMatchKey(route compiledRoute) string {
	return route.route.Match.ExactPath + "\x00" + route.route.Match.PathPrefix + "\x00" + strings.Join(route.route.Match.Methods, ",")
}

func sortedKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func cloneRoute(route Route) Route {
	route.Match.Methods = append([]string(nil), route.Match.Methods...)
	return route
}

func containsProtectedRoute(routes []Route) bool {
	for _, route := range routes {
		if route.Access == AccessProtected {
			return true
		}
	}
	return false
}

// RouteFromContext 返回 SDK 选中的路由副本。
func RouteFromContext(ctx context.Context) (Route, bool) {
	route, ok := ctx.Value(routeContextKey).(Route)
	return cloneRoute(route), ok
}
