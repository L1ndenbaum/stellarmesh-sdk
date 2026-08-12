package gateway

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type reverseProxyResolver struct {
	proxies map[string]http.Handler
}

func newReverseProxyResolver(upstreams []Upstream, transport http.RoundTripper, responder ErrorResponder) (*reverseProxyResolver, error) {
	if len(upstreams) == 0 {
		return nil, errors.New("gateway upstreams are required")
	}
	if transport == nil {
		transport = defaultProxyTransport()
	}
	resolver := &reverseProxyResolver{proxies: make(map[string]http.Handler, len(upstreams))}
	for index, upstream := range upstreams {
		name := strings.TrimSpace(upstream.Name)
		if name == "" {
			return nil, errors.New("gateway upstream name is required at index " + strconv.Itoa(index))
		}
		if _, exists := resolver.proxies[name]; exists {
			return nil, errors.New("duplicate gateway upstream: " + name)
		}
		target, err := url.Parse(strings.TrimSpace(upstream.URL))
		if err != nil || (target.Scheme != "http" && target.Scheme != "https") || target.Host == "" {
			return nil, errors.New("invalid gateway upstream URL: " + name)
		}
		resolver.proxies[name] = newReverseProxy(target, transport, responder)
	}
	return resolver, nil
}

func (resolver *reverseProxyResolver) ResolveUpstream(route Route) (http.Handler, error) {
	proxy, ok := resolver.proxies[route.Upstream]
	if !ok {
		return nil, errors.New("gateway upstream is not configured: " + route.Upstream)
	}
	return proxy, nil
}

func newReverseProxy(target *url.URL, transport http.RoundTripper, responder ErrorResponder) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Rewrite: func(request *httputil.ProxyRequest) {
			request.SetURL(target)
			request.Out.Host = target.Host
			request.SetXForwarded()
			if clientIP, ok := ClientIPFromContext(request.In.Context()); ok {
				request.Out.Header.Set("X-Forwarded-For", clientIP)
			}
		},
		Transport: transport,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			status := http.StatusBadGateway
			code := "upstream_unavailable"
			message := "upstream unavailable"
			var maxBytesError *http.MaxBytesError
			var netError net.Error
			switch {
			case errors.As(err, &maxBytesError):
				status = http.StatusRequestEntityTooLarge
				code = "request_body_too_large"
				message = "request body too large"
			case errors.Is(err, context.DeadlineExceeded), errors.As(err, &netError) && netError.Timeout():
				status = http.StatusGatewayTimeout
				code = "upstream_timeout"
				message = "upstream timeout"
			}
			gatewayError := GatewayError{Status: status, Code: code, Message: message, Cause: err}
			recordGatewayError(r, gatewayError)
			responder.Respond(w, r, gatewayError)
		},
	}
}

func defaultProxyTransport() *http.Transport {
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   5 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
}
