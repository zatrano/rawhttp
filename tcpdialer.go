package rawhttp

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// DefaultDialTimeout is used by TCPDialer when timeout is zero.
	DefaultDialTimeout = 3 * time.Second
	// DefaultDNSCacheDuration is the default TCPDialer DNS cache TTL.
	DefaultDNSCacheDuration = time.Minute
)

// Resolver looks up host IP addresses (net.Resolver compatible).
type Resolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

// TCPDialer dials TCP addresses with optional DNS caching and round-robin.
// Pass Dial / DialTimeout / DialDualStack results to Client.Dial or HostClient.Dial.
type TCPDialer struct {
	// Resolver overrides DNS lookup. nil → net.DefaultResolver.
	Resolver Resolver
	// LocalAddr binds the local end of dialed connections.
	LocalAddr *net.TCPAddr
	// Concurrency caps concurrent dials (0 = unlimited). Set before first Dial.
	Concurrency int
	// DNSCacheDuration overrides DefaultDNSCacheDuration (0 → default).
	DNSCacheDuration time.Duration
	// DisableDNSResolution dials addr as-is without LookupIPAddr.
	DisableDNSResolution bool

	once          sync.Once
	concurrencyCh chan struct{}
	cache         sync.Map // host → *dnsCacheEntry
}

type dnsCacheEntry struct {
	addrs []net.IPAddr
	until time.Time
	rr    atomic.Uint64
}

var defaultTCPDialer = &TCPDialer{Concurrency: 1000}

// Dial dials addr (host:port) using tcp4 with DNS cache.
func Dial(addr string) (net.Conn, error) {
	return defaultTCPDialer.Dial(addr)
}

// DialTimeout dials addr using tcp4 with the given timeout.
func DialTimeout(addr string, timeout time.Duration) (net.Conn, error) {
	return defaultTCPDialer.DialTimeout(addr, timeout)
}

// DialDualStack dials addr using tcp (IPv4+IPv6) with DNS cache.
func DialDualStack(addr string) (net.Conn, error) {
	return defaultTCPDialer.DialDualStack(addr)
}

// DialDualStackTimeout dials addr using tcp with the given timeout.
func DialDualStackTimeout(addr string, timeout time.Duration) (net.Conn, error) {
	return defaultTCPDialer.DialDualStackTimeout(addr, timeout)
}

// Dial dials addr using tcp4.
func (d *TCPDialer) Dial(addr string) (net.Conn, error) {
	return d.dial(addr, false, DefaultDialTimeout)
}

// DialTimeout dials addr using tcp4 with timeout.
func (d *TCPDialer) DialTimeout(addr string, timeout time.Duration) (net.Conn, error) {
	if timeout <= 0 {
		timeout = DefaultDialTimeout
	}
	return d.dial(addr, false, timeout)
}

// DialDualStack dials addr using tcp (dual stack).
func (d *TCPDialer) DialDualStack(addr string) (net.Conn, error) {
	return d.dial(addr, true, DefaultDialTimeout)
}

// DialDualStackTimeout dials addr using tcp with timeout.
func (d *TCPDialer) DialDualStackTimeout(addr string, timeout time.Duration) (net.Conn, error) {
	if timeout <= 0 {
		timeout = DefaultDialTimeout
	}
	return d.dial(addr, true, timeout)
}

// DialFunc returns a Client/HostClient-compatible dialer (tcp4 or dual-stack).
func (d *TCPDialer) DialFunc(dualStack bool) func(network, addr string) (net.Conn, error) {
	return func(network, addr string) (net.Conn, error) {
		_ = network
		if dualStack {
			return d.DialDualStack(addr)
		}
		return d.Dial(addr)
	}
}

func (d *TCPDialer) dial(addr string, dualStack bool, timeout time.Duration) (net.Conn, error) {
	d.once.Do(func() {
		if d.Concurrency > 0 {
			d.concurrencyCh = make(chan struct{}, d.Concurrency)
		}
	})
	if d.concurrencyCh != nil {
		d.concurrencyCh <- struct{}{}
		defer func() { <-d.concurrencyCh }()
	}

	network := "tcp4"
	if dualStack {
		network = "tcp"
	}

	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}

	deadline := time.Now().Add(timeout)
	dialer := net.Dialer{Timeout: timeout, LocalAddr: tcpAddrAsAddr(d.LocalAddr)}

	if d.DisableDNSResolution || net.ParseIP(host) != nil {
		ctx, cancel := context.WithDeadline(context.Background(), deadline)
		defer cancel()
		return dialer.DialContext(ctx, network, addr)
	}

	addrs, err := d.lookup(host)
	if err != nil {
		return nil, err
	}
	if len(addrs) == 0 {
		return nil, &net.OpError{Op: "dial", Net: network, Addr: nil, Err: errNoSuchHost}
	}

	var lastErr error
	n := len(addrs)
	start := int(d.rrIndex(host, addrs) % uint64(n)) //nolint:gosec // n > 0
	for i := 0; i < n; i++ {
		ip := addrs[(start+i)%n].IP
		if !dualStack && ip.To4() == nil {
			continue
		}
		target := net.JoinHostPort(ip.String(), port)
		rem := time.Until(deadline)
		if rem <= 0 {
			return nil, ErrTimeout
		}
		dialer.Timeout = rem
		ctx, cancel := context.WithDeadline(context.Background(), deadline)
		conn, err := dialer.DialContext(ctx, network, target)
		cancel()
		if err == nil {
			return conn, nil
		}
		lastErr = err
		if isTimeoutErr(err) {
			return nil, ErrTimeout
		}
	}
	if lastErr == nil {
		lastErr = errNoSuchHost
	}
	return nil, lastErr
}

var errNoSuchHost = &net.DNSError{Err: "no such host", IsNotFound: true}

func (d *TCPDialer) lookup(host string) ([]net.IPAddr, error) {
	ttl := d.DNSCacheDuration
	if ttl <= 0 {
		ttl = DefaultDNSCacheDuration
	}
	if v, ok := d.cache.Load(host); ok {
		e := v.(*dnsCacheEntry)
		if time.Now().Before(e.until) {
			return e.addrs, nil
		}
	}
	resolver := d.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	ctx, cancel := context.WithTimeout(context.Background(), DefaultDialTimeout)
	defer cancel()
	addrs, err := resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	e := &dnsCacheEntry{addrs: addrs, until: time.Now().Add(ttl)}
	d.cache.Store(host, e)
	return addrs, nil
}

func (d *TCPDialer) rrIndex(host string, addrs []net.IPAddr) uint64 {
	if v, ok := d.cache.Load(host); ok {
		e := v.(*dnsCacheEntry)
		return e.rr.Add(1) - 1
	}
	return 0
}

func tcpAddrAsAddr(a *net.TCPAddr) net.Addr {
	if a == nil {
		return nil
	}
	return a
}
