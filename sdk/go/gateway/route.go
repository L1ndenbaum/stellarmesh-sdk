package gateway

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"slices"
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
	// ExactPath、ExactPaths、PathPrefix、PathTemplate 和 PathRegexp 必须且只能设置一种。
	ExactPath string
	// PathPrefix 按字符串前缀匹配，优先选择最长前缀；需要路径段边界时请显式包含斜杠。
	PathPrefix string
	// ExactPaths 为同一策略声明多条精确路径。
	ExactPaths []string
	// PathTemplate 支持整段 {name}、* 及末尾 **；命名段通过 Request.PathValue 读取。
	PathTemplate string
	// PathRegexp 使用 Go 正则对整个解码路径进行匹配。
	PathRegexp string
	// Priority 仅用于模板和正则，值较大者优先；同优先级的重叠匹配拒绝转发。
	Priority int
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
	// TimeoutProfile 引用固定上游的超时配置；空值沿用共享 Transport。
	TimeoutProfile string
}

// RouteResolver 把请求解析为一个项目声明的路由。
type RouteResolver interface {
	Resolve(*http.Request) (Route, bool, error)
}

// RouteResolverFunc 让函数直接实现 RouteResolver。
type RouteResolverFunc func(*http.Request) (Route, bool, error)

type staticRouteResolver struct {
	exact    map[string][]compiledRoute
	patterns []compiledRoute
	prefixes []compiledRoute
}

type compiledRoute struct {
	route   Route
	methods map[string]struct{}
	pattern *regexp.Regexp
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
	resolver := &staticRouteResolver{exact: make(map[string][]compiledRoute)}
	seen := make(map[string]struct{}, len(routes))
	for index, route := range routes {
		compiled, err := compileRoute(route)
		if err != nil {
			return nil, errors.New("invalid gateway route at index " + strconv.Itoa(index) + ": " + err.Error())
		}
		match := compiled.route.Match
		paths := match.ExactPaths
		if match.ExactPath != "" {
			paths = []string{match.ExactPath}
		}
		var keys []string
		switch {
		case len(paths) > 0:
			for _, path := range paths {
				keys = append(keys, "exact:"+path)
				resolver.exact[path] = append(resolver.exact[path], compiled)
			}
		case compiled.pattern != nil:
			keys = []string{"pattern:" + compiled.pattern.String()}
			resolver.patterns = append(resolver.patterns, compiled)
		default:
			keys = []string{"prefix:" + match.PathPrefix}
			resolver.prefixes = append(resolver.prefixes, compiled)
		}
		for _, key := range keys {
			key += "\x00" + strings.Join(match.Methods, ",")
			if _, ok := seen[key]; ok {
				return nil, errors.New("duplicate gateway route match: " + route.Name)
			}
			seen[key] = struct{}{}
		}
	}
	sort.SliceStable(resolver.patterns, func(i, j int) bool {
		return resolver.patterns[i].route.Match.Priority > resolver.patterns[j].route.Match.Priority
	})
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
	pattern, err := compilePathMatch(&route.Match)
	if err != nil {
		return compiledRoute{}, err
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
	return compiledRoute{route: route, methods: methods, pattern: pattern}, nil
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
	if len(resolver.patterns) > 0 && ambiguousPath(r.URL) {
		return Route{}, false, nil
	}
	for _, route := range resolver.exact[r.URL.Path] {
		if route.accepts(r.Method) {
			return cloneRoute(route.route), true, nil
		}
	}
	var selected *compiledRoute
	for i := range resolver.patterns {
		candidate := &resolver.patterns[i]
		if selected != nil && candidate.route.Match.Priority < selected.route.Match.Priority {
			break
		}
		if candidate.accepts(r.Method) && candidate.pattern.MatchString(r.URL.Path) {
			if selected != nil {
				return Route{}, false, errors.New("ambiguous gateway path patterns")
			}
			selected = candidate
		}
	}
	if selected != nil {
		if selected.route.Match.PathTemplate != "" && selected.pattern.NumSubexp() > 0 {
			values := selected.pattern.FindStringSubmatch(r.URL.Path)
			for i, name := range selected.pattern.SubexpNames() {
				if name != "" {
					r.SetPathValue(name, values[i])
				}
			}
		}
		return cloneRoute(selected.route), true, nil
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

func sortedKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func cloneRoute(route Route) Route {
	route.Match.Methods = slices.Clone(route.Match.Methods)
	route.Match.ExactPaths = slices.Clone(route.Match.ExactPaths)
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
