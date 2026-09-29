package rawhttp

import (
	"errors"
	"net"
	"strings"
	"sync"
)

// ErrPerIPConnLimit is returned from ServeConn when MaxConnsPerIP is exceeded.
var ErrPerIPConnLimit = errors.New("rawhttp: too many connections per ip")

type perIPCounter struct {
	mu sync.Mutex
	m  map[string]int
}

func (c *perIPCounter) tryAcquire(ip string, max int) bool {
	if ip == "" || max <= 0 {
		return true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = make(map[string]int)
	}
	if c.m[ip] >= max {
		return false
	}
	c.m[ip]++
	return true
}

func (c *perIPCounter) release(ip string) {
	if ip == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	n := c.m[ip]
	if n <= 1 {
		delete(c.m, ip)
		return
	}
	c.m[ip] = n - 1
}

func connRemoteIP(conn net.Conn) string {
	if conn == nil {
		return ""
	}
	ra := conn.RemoteAddr()
	if ra == nil {
		return ""
	}
	s := ra.String()
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
