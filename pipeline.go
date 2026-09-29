package rawhttp

import (
	"crypto/tls"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// PipelineClient is an HTTP/1.1 client optimized for a single high-throughput host.
// It shares HostClient connection pooling with a pending-request cap (fasthttp parity).
type PipelineClient struct {
	Addr                     string
	IsTLS                    bool
	Name                     string
	NoDefaultUserAgentHeader bool
	DialTimeout              time.Duration
	ReadTimeout              time.Duration
	WriteTimeout             time.Duration
	MaxResponseBodySize      int
	MaxConns                 int
	MaxPendingRequests       int
	TLSConfig                *tls.Config

	hc       HostClient
	mu       sync.Mutex
	pending  int
	initOnce sync.Once
}

func (pc *PipelineClient) init() {
	pc.initOnce.Do(func() {
		pc.hc = HostClient{
			Addr:                     pc.Addr,
			IsTLS:                    pc.IsTLS,
			Name:                     pc.Name,
			NoDefaultUserAgentHeader: pc.NoDefaultUserAgentHeader,
			DialTimeout:              pc.DialTimeout,
			ReadTimeout:              pc.ReadTimeout,
			WriteTimeout:             pc.WriteTimeout,
			MaxResponseBodySize:      pc.MaxResponseBodySize,
			MaxConns:                 pc.MaxConns,
			TLSConfig:                pc.TLSConfig,
		}
		if pc.MaxPendingRequests <= 0 {
			pc.MaxPendingRequests = 1024
		}
	})
}

// Do sends req and fills resp. Returns an error if MaxPendingRequests is exceeded.
func (pc *PipelineClient) Do(req *Request, resp *Response) error {
	pc.init()
	pc.mu.Lock()
	if pc.pending >= pc.MaxPendingRequests {
		pc.mu.Unlock()
		return errors.New("rawhttp: pipeline client pending limit")
	}
	pc.pending++
	pc.mu.Unlock()
	defer func() {
		pc.mu.Lock()
		pc.pending--
		pc.mu.Unlock()
	}()
	return pc.hc.Do(req, resp)
}

// CloseIdleConnections closes idle keep-alive connections.
func (pc *PipelineClient) CloseIdleConnections() {
	pc.init()
	pc.hc.CloseIdleConnections()
}

// LBClient load-balances Do calls across multiple HostClients (round-robin).
type LBClient struct {
	// Clients are the backends. Must be non-empty before Do.
	Clients []*HostClient
	// Timeout, if >0, is applied as a per-attempt deadline.
	Timeout time.Duration

	rr uint64
}

// Do picks the next HostClient and executes the request.
func (lb *LBClient) Do(req *Request, resp *Response) error {
	n := len(lb.Clients)
	if n == 0 {
		return errors.New("rawhttp: LBClient has no backends")
	}
	i := (atomic.AddUint64(&lb.rr, 1) - 1) % uint64(n)
	hc := lb.Clients[i]
	if hc == nil {
		return errors.New("rawhttp: LBClient nil backend")
	}
	if lb.Timeout > 0 {
		return hc.doDeadline(req, resp, time.Now().Add(lb.Timeout))
	}
	return hc.Do(req, resp)
}

// CloseIdleConnections closes idle connections on all backends.
func (lb *LBClient) CloseIdleConnections() {
	for _, hc := range lb.Clients {
		if hc != nil {
			hc.CloseIdleConnections()
		}
	}
}
