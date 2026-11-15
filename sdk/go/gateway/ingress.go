package gateway

import (
	"errors"
	"net/http"
	"net/netip"
	"strings"
)

// IngressConfig 在 CORS、健康检查和业务路由之前限制入口。非空条件必须同时满足。
type IngressConfig struct {
	// AllowedPeers 接受直接对端 IP 或 CIDR，与客户端 IP 的代理链解析独立。
	AllowedPeers []string
	// AllowedHosts 按 HTTP Host 匹配，忽略大小写，保留显式端口。
	AllowedHosts []string
	// RequireForwardedHTTPS 仅接受允许对端发送的单个 https 标识，需要配置 AllowedPeers。
	RequireForwardedHTTPS bool
}

type ingressPolicy struct {
	peers        []netip.Prefix
	hosts        map[string]bool
	requireHTTPS bool
}

// WithIngress 限制请求可从哪些入口到达网关；缺省允许所有入口。
func WithIngress(input IngressConfig) Option {
	return componentOption("ingress", func(c *config) error {
		if len(input.AllowedPeers) == 0 && (input.RequireForwardedHTTPS || len(input.AllowedHosts) == 0) {
			return errors.New("gateway ingress requires allowed peers or hosts; forwarded HTTPS requires allowed peers")
		}
		policy := &ingressPolicy{requireHTTPS: input.RequireForwardedHTTPS, hosts: make(map[string]bool)}
		for _, peer := range input.AllowedPeers {
			peer = strings.TrimSpace(peer)
			prefix, err := netip.ParsePrefix(peer)
			if err != nil {
				addr, err := netip.ParseAddr(peer)
				if err != nil {
					return errors.New("invalid gateway ingress peer: " + peer)
				}
				addr = addr.Unmap()
				prefix = netip.PrefixFrom(addr, addr.BitLen())
			}
			policy.peers = append(policy.peers, prefix.Masked())
		}
		for _, host := range input.AllowedHosts {
			host = strings.ToLower(strings.TrimSpace(host))
			if !validAuthority(host) {
				return errors.New("invalid gateway ingress host: " + host)
			}
			policy.hosts[host] = true
		}
		c.ingress = policy
		return nil
	})
}

func (p *ingressPolicy) allows(r *http.Request) bool {
	if len(p.hosts) > 0 && !p.hosts[strings.ToLower(r.Host)] {
		return false
	}
	if p.requireHTTPS {
		proto := r.Header.Values("X-Forwarded-Proto")
		if len(proto) != 1 || proto[0] != "https" {
			return false
		}
	}
	if len(p.peers) == 0 {
		return true
	}
	addr, err := parseRemoteAddr(r.RemoteAddr)
	if err != nil {
		return false
	}
	for _, prefix := range p.peers {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}
