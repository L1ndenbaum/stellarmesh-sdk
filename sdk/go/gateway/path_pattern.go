package gateway

import (
	"errors"
	"net/url"
	"regexp"
	"regexp/syntax"
	"slices"
	"strings"
)

var pathParameterName = regexp.MustCompile("^[A-Za-z_][A-Za-z0-9_]*$")

func compilePathMatch(match *RouteMatch) (*regexp.Regexp, error) {
	kinds := 0
	for _, set := range []bool{match.ExactPath != "", len(match.ExactPaths) > 0, match.PathPrefix != "", match.PathTemplate != "", match.PathRegexp != ""} {
		if set {
			kinds++
		}
	}
	if kinds != 1 {
		return nil, errors.New("exactly one gateway path matcher is required")
	}
	switch {
	case match.PathRegexp != "":
		// 先解析完整表达式，避免配置中的括号提前结束外层分组、逃逸整路径锚点。
		expr, err := syntax.Parse(match.PathRegexp, syntax.Perl)
		if err != nil {
			return nil, err
		}
		return regexp.Compile(`\A(?:` + expr.String() + `)\z`)
	case match.PathTemplate != "":
		return compileTemplate(match.PathTemplate)
	}
	if match.Priority != 0 {
		return nil, errors.New("route priority requires a path pattern")
	}
	match.ExactPaths = slices.Clone(match.ExactPaths)
	for i, path := range match.ExactPaths {
		match.ExactPaths[i] = strings.TrimSpace(path)
		if !strings.HasPrefix(match.ExactPaths[i], "/") {
			return nil, errors.New("route path must start with slash")
		}
	}
	if len(match.ExactPaths) == 0 && !strings.HasPrefix(match.ExactPath+match.PathPrefix, "/") {
		return nil, errors.New("route path must start with slash")
	}
	return nil, nil
}

func compileTemplate(template string) (*regexp.Regexp, error) {
	if !strings.HasPrefix(template, "/") || ambiguousPath(&url.URL{Path: template}) {
		return nil, errors.New("invalid gateway path template")
	}
	var expr strings.Builder
	expr.WriteString(`\A`)
	segments := strings.Split(template[1:], "/")
	names := map[string]bool{}
	for index, segment := range segments {
		switch {
		case segment == "**":
			if index != len(segments)-1 {
				return nil, errors.New("** must end a path template")
			}
			if index == 0 {
				expr.WriteString("/.*")
			} else {
				expr.WriteString("(?:/.*)?")
			}
		case segment == "*":
			expr.WriteString("/[^/]+")
		case strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}"):
			name := segment[1 : len(segment)-1]
			if !pathParameterName.MatchString(name) || names[name] {
				return nil, errors.New("invalid or repeated path parameter")
			}
			names[name] = true
			expr.WriteString("/(?P<" + name + ">[^/]+)")
		default:
			if strings.ContainsAny(segment, "{}*") {
				return nil, errors.New("path template wildcard must occupy an entire segment")
			}
			expr.WriteString("/" + regexp.QuoteMeta(segment))
		}
	}
	expr.WriteString(`\z`)
	return regexp.Compile(expr.String())
}

func ambiguousPath(u *url.URL) bool {
	// 先判定编码斜杠，再用解码路径检查遍历段。拒绝时也不回落到较宽的前缀。
	if strings.Contains(strings.ToLower(u.EscapedPath()), "%2f") || strings.ContainsAny(u.Path, "\\\r\n\x00") || strings.Contains(u.Path, "//") {
		return true
	}
	for _, segment := range strings.Split(u.Path, "/") {
		if segment == "." || segment == ".." {
			return true
		}
	}
	return false
}
