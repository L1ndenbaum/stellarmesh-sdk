package gateway

import (
	"errors"
	"net/http"
	"sort"
	"strings"
)

type staticRouteResolver struct {
	exact    []compiledRoute
	prefixes []compiledRoute
}

type compiledRoute struct {
	route   Route
	methods map[string]struct{}
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
			return nil, errors.New("invalid gateway route at index " + itoa(index) + ": " + err.Error())
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
		if method == "" {
			return compiledRoute{}, errors.New("route method cannot be empty")
		}
		methods[method] = struct{}{}
	}
	route.Match.Methods = sortedKeys(methods)
	return compiledRoute{route: route, methods: methods}, nil
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

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := make([]byte, 0, 8)
	for value > 0 {
		digits = append(digits, byte('0'+value%10))
		value /= 10
	}
	for left, right := 0, len(digits)-1; left < right; left, right = left+1, right-1 {
		digits[left], digits[right] = digits[right], digits[left]
	}
	return string(digits)
}
