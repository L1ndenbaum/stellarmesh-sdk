package gateway

import (
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

type trustedProxyResolver struct {
	prefixes []netip.Prefix
}

// WithTrustedProxies 只在直接对端命中声明的 CIDR 时信任转发头。
func WithTrustedProxies(cidrs ...string) Option {
	return componentOption("client_ip_resolver", func(config *config) error {
		if len(cidrs) == 0 {
			return errors.New("gateway trusted proxy CIDRs are required")
		}
		prefixes := make([]netip.Prefix, 0, len(cidrs))
		for _, cidr := range cidrs {
			prefix, err := netip.ParsePrefix(strings.TrimSpace(cidr))
			if err != nil {
				return errors.New("invalid gateway trusted proxy CIDR: " + cidr)
			}
			prefixes = append(prefixes, prefix.Masked())
		}
		config.clientIPResolver = &trustedProxyResolver{prefixes: prefixes}
		return nil
	})
}

func (resolver *trustedProxyResolver) Resolve(r *http.Request) (string, error) {
	remote, err := parseRemoteAddr(r.RemoteAddr)
	if err != nil {
		return "", err
	}
	if !resolver.trusted(remote) {
		return remote.String(), nil
	}
	forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-For"))
	if forwarded == "" {
		realIP := strings.TrimSpace(r.Header.Get("X-Real-IP"))
		if realIP == "" {
			return remote.String(), nil
		}
		address, parseErr := netip.ParseAddr(realIP)
		if parseErr != nil {
			return "", errors.New("invalid X-Real-IP from trusted proxy")
		}
		return address.Unmap().String(), nil
	}
	parts := strings.Split(forwarded, ",")
	chain := make([]netip.Addr, 0, len(parts))
	for _, part := range parts {
		address, parseErr := netip.ParseAddr(strings.TrimSpace(part))
		if parseErr != nil {
			return "", errors.New("invalid X-Forwarded-For from trusted proxy")
		}
		chain = append(chain, address.Unmap())
	}
	for index := len(chain) - 1; index >= 0; index-- {
		if !resolver.trusted(chain[index]) {
			return chain[index].String(), nil
		}
	}
	return chain[0].String(), nil
}

func (resolver *trustedProxyResolver) trusted(address netip.Addr) bool {
	for _, prefix := range resolver.prefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

func parseRemoteAddr(remoteAddr string) (netip.Addr, error) {
	host, _, err := net.SplitHostPort(strings.TrimSpace(remoteAddr))
	if err != nil {
		host = strings.TrimSpace(remoteAddr)
	}
	address, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, errors.New("invalid remote address")
	}
	return address.Unmap(), nil
}
