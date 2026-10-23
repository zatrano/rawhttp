package rawhttp

import (
	"net"
	"strings"
)

// ClientIP returns the client address for access control / logging.
// When Server.TrustedProxies is empty, or the peer is not a trusted proxy,
// this is RemoteIP().
//
// When the peer is trusted, the X-Forwarded-For hops (all header lines, in
// order) are read from the right, skipping hops that are themselves trusted
// proxies. The first untrusted hop is the client. Hops to its left were
// written by the client and are not used. A hop that is not an IP address
// stops the walk and RemoteIP() is returned. When every hop is trusted, the
// leftmost one is returned. Without X-Forwarded-For, a valid X-Real-IP set by
// the trusted peer is used.
func (c *Ctx) ClientIP() string {
	peer := c.RemoteIP()
	if len(c.trustedProxies) == 0 {
		return peer
	}
	if !ipTrusted(peer, c.trustedProxies) {
		return peer
	}
	var hops []string
	c.VisitHeader(func(k, v []byte) {
		if len(k) == len("X-Forwarded-For") && equalFoldStr(k, "X-Forwarded-For") {
			for _, part := range strings.Split(string(v), ",") {
				hops = append(hops, strings.TrimSpace(part))
			}
		}
	})
	if len(hops) > 0 {
		leftmost := ""
		for i := len(hops) - 1; i >= 0; i-- {
			ip := stripIPPort(hops[i])
			if net.ParseIP(ip) == nil {
				return peer
			}
			if !ipTrusted(ip, c.trustedProxies) {
				return ip
			}
			leftmost = ip
		}
		return leftmost
	}
	if xri := c.Header("X-Real-IP"); len(xri) > 0 {
		ip := stripIPPort(strings.TrimSpace(string(xri)))
		if net.ParseIP(ip) != nil {
			return ip
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
