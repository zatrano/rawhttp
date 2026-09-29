package rawhttp

import (
	"net"
	"strings"
)

// ClientIP returns the client address for access control / logging.
// When Server.TrustedProxies is empty, this is RemoteIP().
// When set and the peer is trusted, the left-most X-Forwarded-For hop
// (or X-Real-IP) is used; otherwise RemoteIP().
func (c *Ctx) ClientIP() string {
	peer := c.RemoteIP()
	if len(c.trustedProxies) == 0 {
		return peer
	}
	if !ipTrusted(peer, c.trustedProxies) {
		return peer
	}
	if xff := c.Header("X-Forwarded-For"); len(xff) > 0 {
		parts := strings.Split(string(xff), ",")
		if len(parts) > 0 {
			ip := strings.TrimSpace(parts[0])
			if ip != "" {
				return stripIPPort(ip)
			}
		}
	}
	if xri := c.Header("X-Real-IP"); len(xri) > 0 {
		ip := strings.TrimSpace(string(xri))
		if ip != "" {
			return stripIPPort(ip)
		}
	}
	return peer
}

func stripIPPort(s string) string {
	if s == "" {
		return ""
	}
	if s[0] == '[' {
		if i := strings.IndexByte(s, ']'); i > 0 {
			return s[1:i]
		}
		return s
	}
	if host, _, err := net.SplitHostPort(s); err == nil {
		return host
	}
	return s
}

func parseTrustedProxies(list []string) []*net.IPNet {
	if len(list) == 0 {
		return nil
	}
	out := make([]*net.IPNet, 0, len(list))
	for _, e := range list {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if !strings.Contains(e, "/") {
			ip := net.ParseIP(e)
			if ip == nil {
				continue
			}
			bits := 32
			if ip.To4() == nil {
				bits = 128
			}
			out = append(out, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
			continue
		}
		_, n, err := net.ParseCIDR(e)
		if err != nil {
			continue
		}
		out = append(out, n)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func ipTrusted(ipStr string, nets []*net.IPNet) bool {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}
