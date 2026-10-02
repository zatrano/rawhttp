package rawhttp

import (
	"bufio"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ConnPoolStrategyType selects idle connection reuse order.
type ConnPoolStrategyType int

const (
	// LIFO reuses the most recently released connection (default; cache-friendly).
	LIFO ConnPoolStrategyType = iota
	// FIFO reuses the oldest idle connection first.
	FIFO
)

// ErrConnPoolStrategyNotImpl is returned when ConnPoolStrategy is not LIFO or FIFO.
var ErrConnPoolStrategyNotImpl = errors.New("rawhttp: connection pool strategy is not implemented")

// HostClient is an HTTP/1.1 client bound to a single host with a keep-alive pool.
type HostClient struct {
	// Addr is host or host:port (port defaults: 80 / 443).
	Addr string
	// IsTLS enables TLS for connections to Addr.
	IsTLS bool

	Name                     string
	NoDefaultUserAgentHeader bool
	DialTimeout              time.Duration
	ReadTimeout              time.Duration
	WriteTimeout             time.Duration
	MaxResponseBodySize      int
	MaxConns                 int
	MaxIdleConnDuration      time.Duration
	// MaxConnWaitTimeout is how long acquire waits for a free pooled connection
	// when MaxConns is reached. Zero means wait forever. Positive → ErrNoFreeConns
	// after the wait elapses.
	MaxConnWaitTimeout time.Duration
	// MaxConnDuration closes pooled connections older than this from dial time.
	// Zero means unlimited.
	MaxConnDuration time.Duration
	// MaxResponseHeaderBytes caps response header block size. Zero → 64KiB.
	MaxResponseHeaderBytes int
	// MaxHeaders caps response header count. Zero → 100.
	MaxHeaders int
	// MaxChunks caps chunked response frames. Zero → 16384.
	MaxChunks int
	// MaxIdemponentCallAttempts retries idempotent methods on connection errors
	// (compatibility name). Zero → 5. Negative → 1 (no retry).
	MaxIdemponentCallAttempts int
	// RetryIfErr decides whether to retry after an error. When set, it replaces
	// the default idempotent-method policy. attempts starts at 1.
	RetryIfErr RetryIfErrFunc
	// Network is the dial network. Empty → "tcp". Use "tcp4" / "tcp6" to pin family.
	Network string
	// ReadBufferSize is the per-connection read buffer. Zero → 8KiB.
	ReadBufferSize int
	// WriteBufferSize, when > 0, batches request writes through a bufio.Writer
	// of that size. Zero keeps direct writes (fastest for small requests).
	WriteBufferSize int
	// ConnPoolStrategy selects idle-conn reuse order. Zero → LIFO (default,
	// cache-friendly). Use FIFO for oldest-first reuse.
	ConnPoolStrategy ConnPoolStrategyType
	// DialDualStack, when true and Network is empty, dials with "tcp" (IPv4+IPv6).
	// When Network is set it always wins. Default Network empty already uses "tcp".
	DialDualStack bool
	// StreamResponseBody, when true, does not buffer the response body; use
	// Response.BodyStream and CloseBodyStream (connection stays checked out).
	StreamResponseBody bool
	// DisablePathNormalizing is accepted for Client parity (HostClient uses
	// origin-form RequestURI as-is).
	DisablePathNormalizing bool
	TLSConfig              *tls.Config
	Dial                   func(network, addr string) (net.Conn, error)

	mu       sync.Mutex
	idle     []*clientConn
	conns    int
	cond     *sync.Cond
	initOnce sync.Once
	// sticky is a lock-free one-slot idle cache for the sequential Do hot path.
	sticky atomic.Pointer[clientConn]
	// pending is in-flight Do calls (for PendingRequests); simple GET path skips it.
	pending atomic.Int64

	uaOnce     sync.Once
	cachedUA   string
	limOnce    sync.Once
	limBody    int
	limHdrByte int
	limHeaders int
	limChunks  int
}

type clientConn struct {
	raw        net.Conn
	cr         *connReader
	wbuf       []byte // reused request write scratch
	bw         *bufio.Writer
	writeDLSet bool
	readDLSet  bool
	created    time.Time
	lastUse    time.Time

	// Sticky GET request cache (method+uri+host+ua identity).
	cachedReq    []byte
	cacheMethod  string
	cacheURI     string
	cacheHost    string
	cacheUA      string
	cacheNoExtra bool
}

var clientWriteBufPool = sync.Pool{New: func() any {
	b := make([]byte, 0, 512)
	return &b
}}

func (hc *HostClient) init() {
	hc.initOnce.Do(func() {
		hc.cond = sync.NewCond(&hc.mu)
	})
}

// Do sends req against this host and fills resp.
// req.RequestURI should be origin-form ("/path?query").
func (hc *HostClient) Do(req *Request, resp *Response) error {
	if req == nil || resp == nil {
		return errors.New("rawhttp: nil request or response")
	}
	if req.timeout > 0 {
		return hc.doDeadline(req, resp, time.Now().Add(req.timeout))
	}
	hc.init()
	resp.Reset()

	// Hot path: plain GET keep-alive with no deadlines / body / extra headers.
	if (req.Method == "" || req.Method == "GET") &&
		req.bodyStream == nil && len(req.body) == 0 && len(req.headerBuf) == 0 &&
		hc.WriteTimeout == 0 && hc.ReadTimeout == 0 && !hc.StreamResponseBody {
		return hc.retryIdempotent(req, resp, func() error {
			return hc.doSimpleGET(req, resp)
		})
	}
	hc.pending.Add(1)
	defer hc.pending.Add(-1)
	return hc.retryIdempotent(req, resp, func() error {
		return hc.doDeadlineBody(req, resp, time.Time{})
	})
}

// PendingRequests returns in-flight non-simple Do calls (excludes doSimpleGET).
func (hc *HostClient) PendingRequests() int {
	return int(hc.pending.Load())
}

func (hc *HostClient) doDeadline(req *Request, resp *Response, deadline time.Time) error {
	if req == nil || resp == nil {
		return errors.New("rawhttp: nil request or response")
	}
	hc.pending.Add(1)
	defer hc.pending.Add(-1)
	hc.init()
	resp.Reset()
	return hc.retryIdempotent(req, resp, func() error {
		return hc.doDeadlineBody(req, resp, deadline)
	})
}

func (hc *HostClient) doSimpleGET(req *Request, resp *Response) error {
	uri := req.RequestURI
	if uri == "" {
		uri = "/"
	} else if uri[0] != '/' {
		return errors.New("rawhttp: HostClient RequestURI must be origin-form")
	}
	host := req.Host
	if host == "" {
		host = hc.Addr
	}
	// Fast allow: default gate/client path ("/" + HostClient.Addr).
	if uri != "/" || (req.Host != "" && req.Host != hc.Addr) || host != hc.Addr {
		if containsCTLOrCRLF(uri) || containsCTLOrCRLF(host) {
			return ErrHeaderInvalid
		}
		if uri != "/" && (!validRequestTargetAllow([]byte(uri), hc.DisablePathNormalizing) || bytesIndexByteString(uri, ' ') >= 0) {
			return ErrBadRequest
		}
	}

	cc, err := hc.acquire(time.Time{})
	if err != nil {
		return err
	}
	if err := writeClientGETCached(cc, hc, uri, host); err != nil {
		hc.closeConn(cc)
		return err
	}
	mb, mh, mhdr, mc := hc.cachedLimits()
	if err := readClientResponse(cc.cr, resp, mb, mh, mhdr, mc, "GET", nil, nil); err != nil {
		hc.closeConn(cc)
		if isTimeoutErr(err) {
			return ErrTimeout
		}
		return err
	}
	if resp.connClose {
		hc.closeConn(cc)
		return nil
	}
	hc.release(cc)
	return nil
}

func bytesIndexByteString(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

func (hc *HostClient) doDeadlineBody(req *Request, resp *Response, deadline time.Time) error {
	if err := validateClientRequest(req, hc); err != nil {
		return err
	}

	if !deadline.IsZero() && time.Now().After(deadline) {
		return ErrTimeout
	}

	cc, err := hc.acquire(deadline)
	if err != nil {
		return err
	}
	err = hc.roundTrip(cc, req, resp, deadline)
	if err != nil {
		hc.closeConn(cc)
		if isTimeoutErr(err) {
			return ErrTimeout
		}
		return err
	}
	if resp.bodyStream != nil {
		// Connection owned by BodyStream until CloseBodyStream.
		return nil
	}
	if resp.connClose {
		hc.closeConn(cc)
		return nil
	}
	hc.release(cc)
	return nil
}

func validateClientRequest(req *Request, hc *HostClient) error {
	method := req.Method
	if method == "" {
		method = "GET"
	}
	uri := req.RequestURI
	if uri == "" {
		uri = "/"
	}
	if len(uri) == 0 || uri[0] != '/' {
		return errors.New("rawhttp: HostClient RequestURI must be origin-form")
	}
	host := req.Host
	if host == "" {
		host = hc.Addr
	}
	if !validMethod([]byte(method)) || containsCTLOrCRLF(method) {
		return ErrBadRequest
	}
	if containsCTLOrCRLF(uri) || !validRequestTargetAllow([]byte(uri), hc.DisablePathNormalizing) {
		return ErrBadRequest
	}
	if containsCTLOrCRLF(host) {
		return ErrHeaderInvalid
	}
	return nil
}

func isTimeoutErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrTimeout) {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

func (hc *HostClient) clientUA() string {
	hc.uaOnce.Do(func() {
		if hc.NoDefaultUserAgentHeader {
			return
		}
		ua := hc.Name
		if ua == "" {
			ua = defaultClientUserAgent
		}
		hc.cachedUA = ua
	})
	return hc.cachedUA
}

func (hc *HostClient) cachedLimits() (body, hdrBytes, headers, chunks int) {
	hc.limOnce.Do(func() {
		hc.limBody = hc.maxBody()
		hc.limHdrByte = hc.maxRespHeaderBytes()
		hc.limHeaders = hc.maxHeaders()
		hc.limChunks = hc.maxChunks()
	})
	return hc.limBody, hc.limHdrByte, hc.limHeaders, hc.limChunks
}

func (hc *HostClient) maxConns() int {
	n := hc.MaxConns
	if n == 0 {
		n = defaultMaxConnsPerHost
	}
	if n < 0 {
		return 1 << 20
	}
	return n
}

func (hc *HostClient) maxIdle() time.Duration {
	d := hc.MaxIdleConnDuration
	if d == 0 {
		return defaultMaxIdleConnDuration
	}
	if d < 0 {
		return 24 * time.Hour
	}
	return d
}

func (hc *HostClient) connExpired(cc *clientConn, now time.Time) bool {
	d := hc.MaxConnDuration
	if d <= 0 {
		return false
	}
	return now.Sub(cc.created) >= d
}

func (hc *HostClient) maxBody() int {
	n := hc.MaxResponseBodySize
	if n == 0 {
		return defaultMaxResponseBodySize
	}
	if n < 0 {
		return 1 << 30
	}
	return n
}

func (hc *HostClient) maxRespHeaderBytes() int {
	n := hc.MaxResponseHeaderBytes
	if n == 0 {
		return defaultMaxResponseHeaderBytes
	}
	if n < 0 {
		return 1 << 30
	}
	return n
}

func (hc *HostClient) maxIdempotentAttempts() int {
	n := hc.MaxIdemponentCallAttempts
	if n == 0 {
		return defaultIdempotentAttempts
	}
	if n < 0 {
		return 1
	}
	return n
}

func (hc *HostClient) dialNetwork() string {
	if hc.Network != "" {
		return hc.Network
	}
	if hc.DialDualStack {
		return "tcp"
	}
	return "tcp"
}

func (hc *HostClient) retryIdempotent(req *Request, resp *Response, fn func() error) error {
	attempts := 1
	if hc.RetryIfErr != nil || isIdempotentMethod(methodOf(req)) {
		attempts = hc.maxIdempotentAttempts()
	}
	var err error
	for i := 0; i < attempts; i++ {
		if i > 0 {
			resp.Reset()
		}
		err = fn()
		if err == nil {
			return nil
		}
		if hc.RetryIfErr != nil {
			_, retry := hc.RetryIfErr(req, i+1, err)
			if !retry {
				return err
			}
			continue
		}
		if !isIdempotentRetryable(err) {
			return err
		}
	}
	return err
}

func isIdempotentMethod(m string) bool {
	switch m {
	case "", "GET", "HEAD", "PUT", "DELETE", "OPTIONS", "TRACE":
		return true
	default:
		return false
	}
}

func isIdempotentRetryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrTimeout) || errors.Is(err, ErrNoFreeConns) ||
		errors.Is(err, ErrBadRequest) || errors.Is(err, ErrHeaderInvalid) ||
		errors.Is(err, ErrBodyTooLarge) {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return !ne.Timeout()
	}
	msg := err.Error()
	return strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "use of closed network connection")
}

func (hc *HostClient) maxHeaders() int {
	if hc.MaxHeaders > 0 {
		return hc.MaxHeaders
	}
	return defaultMaxHeaders
}

func (hc *HostClient) maxChunks() int {
	if hc.MaxChunks > 0 {
		return hc.MaxChunks
	}
	return maxChunksPerBody
}

func (hc *HostClient) dialTimeout() time.Duration {
	d := hc.DialTimeout
	if d == 0 {
		return defaultClientDialTimeout
	}
	return d
}

func (hc *HostClient) readTimeout() time.Duration {
	// 0 and negative both mean unlimited (HostClient timeout convention for 0).
	return hc.ReadTimeout
}

func (hc *HostClient) writeTimeout() time.Duration {
	return hc.WriteTimeout
}

func (hc *HostClient) acquire(deadline time.Time) (*clientConn, error) {
	maxIdle := hc.maxIdle()
	waitDeadline := time.Time{}
	if hc.MaxConnWaitTimeout > 0 {
		waitDeadline = time.Now().Add(hc.MaxConnWaitTimeout)
	}

	now := time.Now()
	if cc := hc.sticky.Swap(nil); cc != nil {
		if now.Sub(cc.lastUse) <= maxIdle && !hc.connExpired(cc, now) {
			return cc, nil
		}
		_ = cc.raw.Close()
		cc.cr.releaseBuf()
		hc.mu.Lock()
		hc.conns--
		hc.cond.Broadcast()
		hc.mu.Unlock()
	}

	hc.mu.Lock()
	for {
		now = time.Now()
		if !deadline.IsZero() && now.After(deadline) {
			hc.mu.Unlock()
			return nil, ErrTimeout
		}
		if !waitDeadline.IsZero() && now.After(waitDeadline) {
			hc.mu.Unlock()
			return nil, ErrNoFreeConns
		}
		for len(hc.idle) > 0 {
			var cc *clientConn
			switch hc.ConnPoolStrategy {
			case LIFO:
				cc = hc.idle[len(hc.idle)-1]
				hc.idle = hc.idle[:len(hc.idle)-1]
			case FIFO:
				cc = hc.idle[0]
				copy(hc.idle, hc.idle[1:])
				hc.idle[len(hc.idle)-1] = nil
				hc.idle = hc.idle[:len(hc.idle)-1]
			default:
				hc.mu.Unlock()
				return nil, ErrConnPoolStrategyNotImpl
			}
			if now.Sub(cc.lastUse) > maxIdle || hc.connExpired(cc, now) {
				hc.conns--
				hc.mu.Unlock()
				_ = cc.raw.Close()
				cc.cr.releaseBuf()
				hc.mu.Lock()
				continue
			}
			hc.mu.Unlock()
			return cc, nil
		}
		if hc.conns < hc.maxConns() {
			hc.conns++
			hc.mu.Unlock()
			cc, err := hc.dialNew(deadline)
			if err != nil {
				hc.mu.Lock()
				hc.conns--
				hc.cond.Broadcast()
				hc.mu.Unlock()
				return nil, err
			}
			return cc, nil
		}
		// No free conn: wait with optional MaxConnWaitTimeout.
		waitUntil := waitDeadline
		if !deadline.IsZero() && (waitUntil.IsZero() || deadline.Before(waitUntil)) {
			waitUntil = deadline
		}
		if waitUntil.IsZero() {
			hc.cond.Wait()
			continue
		}
		remaining := time.Until(waitUntil)
		if remaining <= 0 {
			hc.mu.Unlock()
			if !deadline.IsZero() && time.Now().After(deadline) {
				return nil, ErrTimeout
			}
			return nil, ErrNoFreeConns
		}
		timer := time.AfterFunc(remaining, func() {
			hc.mu.Lock()
			hc.cond.Broadcast()
			hc.mu.Unlock()
		})
		hc.cond.Wait()
		timer.Stop()
	}
}

func (hc *HostClient) release(cc *clientConn) {
	cc.lastUse = time.Now()
	if hc.sticky.CompareAndSwap(nil, cc) {
		return
	}
	hc.mu.Lock()
	hc.idle = append(hc.idle, cc)
	hc.cond.Signal()
	hc.mu.Unlock()
}

func (hc *HostClient) closeConn(cc *clientConn) {
	_ = cc.raw.Close()
	cc.cr.releaseBuf()
	hc.mu.Lock()
	hc.conns--
	hc.cond.Signal()
	hc.mu.Unlock()
}

func (hc *HostClient) dialAddr() string {
	addr := hc.Addr
	if !strings.Contains(addr, ":") {
		if hc.IsTLS {
			addr += ":443"
		} else {
			addr += ":80"
		}
	}
	return addr
}

func (hc *HostClient) dialNew(deadline time.Time) (*clientConn, error) {
	addr := hc.dialAddr()
	var (
		conn net.Conn
		err  error
	)
	dt := hc.dialTimeout()
	if !deadline.IsZero() {
		rem := time.Until(deadline)
		if rem <= 0 {
			return nil, ErrTimeout
		}
		if dt < 0 || rem < dt {
			dt = rem
		}
	}
	network := hc.dialNetwork()
	if hc.Dial != nil {
		conn, err = hc.Dial(network, addr)
	} else if network == "tcp4" || network == "tcp" {
		timeout := dt
		if timeout < 0 {
			timeout = 24 * time.Hour
		}
		if network == "tcp" {
			conn, err = DialDualStackTimeout(addr, timeout)
		} else {
			conn, err = DialTimeout(addr, timeout)
		}
	} else if dt < 0 {
		conn, err = net.Dial(network, addr)
	} else {
		conn, err = net.DialTimeout(network, addr, dt)
	}
	if err != nil {
		if isTimeoutErr(err) {
			return nil, ErrTimeout
		}
		return nil, err
	}
	if hc.IsTLS {
		cfg := hc.TLSConfig
		if cfg == nil {
			cfg = &tls.Config{MinVersion: tls.VersionTLS12}
		} else {
			cfg = cfg.Clone()
		}
		if cfg.ServerName == "" {
			host, _, splitErr := net.SplitHostPort(addr)
			if splitErr != nil {
				host = hc.Addr
			}
			cfg.ServerName = host
		}
		tc := tls.Client(conn, cfg)
		if dt >= 0 {
			_ = tc.SetDeadline(time.Now().Add(dt))
		}
		if err := tc.Handshake(); err != nil {
			_ = conn.Close()
			if isTimeoutErr(err) {
				return nil, ErrTimeout
			}
			return nil, err
		}
		_ = tc.SetDeadline(time.Time{})
		conn = tc
	}
	now := time.Now()
	bufSize := hc.ReadBufferSize
	if bufSize <= 0 {
		bufSize = defaultBufSize
	}
	cc := &clientConn{
		raw:     conn,
		cr:      newConnReader(conn, bufSize),
		created: now,
		lastUse: now,
	}
	if hc.WriteBufferSize > 0 {
		cc.bw = bufio.NewWriterSize(conn, hc.WriteBufferSize)
	}
	return cc, nil
}

func (hc *HostClient) roundTrip(cc *clientConn, req *Request, resp *Response, deadline time.Time) error {
	writeDL := deadline
	if writeDL.IsZero() {
		wt := hc.writeTimeout()
		if wt > 0 {
			writeDL = time.Now().Add(wt)
		}
	}
	if !writeDL.IsZero() {
		_ = cc.raw.SetWriteDeadline(writeDL)
		cc.writeDLSet = true
	} else if cc.writeDLSet {
		_ = cc.raw.SetWriteDeadline(time.Time{})
		cc.writeDLSet = false
	}
	if err := writeClientRequest(cc, hc, req); err != nil {
		return err
	}

	readDL := deadline
	if readDL.IsZero() {
		rt := hc.readTimeout()
		if rt > 0 {
			readDL = time.Now().Add(rt)
		}
	}
	if !readDL.IsZero() {
		_ = cc.raw.SetReadDeadline(readDL)
		cc.readDLSet = true
	} else if cc.readDLSet {
		_ = cc.raw.SetReadDeadline(time.Time{})
		cc.readDLSet = false
	}
	return readClientResponse(cc.cr, resp, hc.maxBody(), hc.maxRespHeaderBytes(), hc.maxHeaders(), hc.maxChunks(), methodOf(req), hc, cc)
}

func methodOf(req *Request) string {
	if req.Method == "" {
		return "GET"
	}
	return req.Method
}

// writeClientGETCached writes a plain GET (already validated by doSimpleGET).
func writeClientGETCached(cc *clientConn, hc *HostClient, uri, host string) error {
	ua := hc.clientUA()
	if cc.cacheNoExtra && cc.cacheMethod == "GET" && cc.cacheURI == uri &&
		cc.cacheHost == host && cc.cacheUA == ua && len(cc.cachedReq) > 0 {
		_, err := cc.raw.Write(cc.cachedReq)
		return err
	}

	buf := cc.wbuf
	if buf != nil {
		buf = buf[:0]
	} else {
		bp := clientWriteBufPool.Get().(*[]byte)
		buf = (*bp)[:0]
	}
	buf = append(buf, "GET "...)
	buf = append(buf, uri...)
	buf = append(buf, " HTTP/1.1\r\nHost: "...)
	buf = append(buf, host...)
	buf = append(buf, '\r', '\n')
	if ua != "" {
		buf = append(buf, "User-Agent: "...)
		buf = append(buf, ua...)
		buf = append(buf, '\r', '\n')
	}
	// HTTP/1.1 defaults to keep-alive — omit Connection header on the hot path.
	buf = append(buf, '\r', '\n')
	cc.wbuf = buf
	cc.cachedReq = append(cc.cachedReq[:0], buf...)
	cc.cacheMethod = "GET"
	cc.cacheURI = uri
	cc.cacheHost = host
	cc.cacheUA = ua
	cc.cacheNoExtra = true
	_, err := cc.raw.Write(buf)
	return err
}

func writeClientRequest(cc *clientConn, hc *HostClient, req *Request) error {
	method := req.Method
	if method == "" {
		method = "GET"
	}
	uri := req.RequestURI
	if uri == "" {
		uri = "/"
	}
	host := req.Host
	if host == "" {
		host = hc.Addr
	}
	// Request already validated by doDeadlineBody / validateClientRequest.

	ua := ""
	if !hc.NoDefaultUserAgentHeader {
		ua = hc.Name
		if ua == "" {
			ua = defaultClientUserAgent
		}
	}

	// Fast path: cached GET with no body and no extra headers.
	if method == "GET" && len(req.body) == 0 && req.bodyStream == nil && len(req.headerBuf) == 0 &&
		cc.cacheNoExtra && cc.cacheMethod == method && cc.cacheURI == uri &&
		cc.cacheHost == host && cc.cacheUA == ua && len(cc.cachedReq) > 0 {
		_, err := cc.raw.Write(cc.cachedReq)
		return err
	}

	buf := cc.wbuf
	if buf != nil {
		buf = buf[:0]
	} else {
		bp := clientWriteBufPool.Get().(*[]byte)
		buf = (*bp)[:0]
	}
	buf = append(buf, method...)
	buf = append(buf, ' ')
	buf = append(buf, uri...)
	buf = append(buf, " HTTP/1.1\r\nHost: "...)
	buf = append(buf, host...)
	buf = append(buf, '\r', '\n')

	if ua != "" {
		buf = append(buf, "User-Agent: "...)
		buf = append(buf, ua...)
		buf = append(buf, '\r', '\n')
	}

	buf = append(buf, req.headerBuf...)

	stream := req.bodyStream
	body := req.body
	if stream != nil {
		if req.bodyStreamSize < 0 {
			buf = append(buf, "Transfer-Encoding: chunked\r\n"...)
		} else {
			buf = append(buf, "Content-Length: "...)
			buf = strconv.AppendInt(buf, int64(req.bodyStreamSize), 10)
			buf = append(buf, '\r', '\n')
		}
	} else if len(body) > 0 || method == "POST" || method == "PUT" || method == "PATCH" {
		buf = append(buf, "Content-Length: "...)
		buf = strconv.AppendInt(buf, int64(len(body)), 10)
		buf = append(buf, '\r', '\n')
	}
	buf = append(buf, "\r\n"...)
	if stream == nil && len(body) > 0 {
		buf = append(buf, body...)
	}
	cc.wbuf = buf

	if method == "GET" && stream == nil && len(body) == 0 && len(req.headerBuf) == 0 {
		cc.cachedReq = append(cc.cachedReq[:0], buf...)
		cc.cacheMethod = method
		cc.cacheURI = uri
		cc.cacheHost = host
		cc.cacheUA = ua
		cc.cacheNoExtra = true
	} else {
		cc.cacheNoExtra = false
	}

	var w io.Writer = cc.raw
	if cc.bw != nil {
		w = cc.bw
	}
	if _, err := w.Write(buf); err != nil {
		return err
	}
	if stream != nil {
		var err error
		if req.bodyStreamSize < 0 {
			err = writeClientChunkedBody(w, stream)
		} else {
			_, err = io.CopyN(w, stream, int64(req.bodyStreamSize))
		}
		if err != nil {
			return err
		}
	}
	if cc.bw != nil {
		return cc.bw.Flush()
	}
	return nil
}

func writeClientChunkedBody(w io.Writer, r io.Reader) error {
	bufp := streamCopyBufPool.Get().(*[]byte)
	buf := *bufp
	defer streamCopyBufPool.Put(bufp)
	var hdr [32]byte
	for {
		n, err := r.Read(buf)
		if n > 0 {
			h := strconv.AppendInt(hdr[:0], int64(n), 16)
			h = append(h, '\r', '\n')
			if _, e := w.Write(h); e != nil {
				return e
			}
			if _, e := w.Write(buf[:n]); e != nil {
				return e
			}
			if _, e := w.Write([]byte{'\r', '\n'}); e != nil {
				return e
			}
		}
		if err == io.EOF {
			_, e := w.Write([]byte("0\r\n\r\n"))
			return e
		}
		if err != nil {
			return err
		}
	}
}

func readClientResponse(cr *connReader, resp *Response, maxBody, maxHdrBytes, maxHeaders, maxChunks int, method string, hc *HostClient, cc *clientConn) error {
	const maxInformational = 5
	n1xx := 0
	streamBody := hc != nil && hc.StreamResponseBody && cc != nil
	// Skip 1xx informational responses.
	for {
		// Reclaim bytes from prior responses so a straddling status+headers
		// block can compact before the pin advances past the status line.
		if cr.r > 0 && cr.r == cr.off {
			cr.compact()
		}

		line, err := cr.readLine()
		if err != nil {
			return err
		}
		code, ok := parseStatusLine(line)
		if !ok {
			return ErrBadRequest
		}
		resp.StatusCode = code
		// Status line is fully consumed and not retained as a buffer slice;
		// unpin so fill() can compact if headers straddle the buffer end.
		cr.release()
		if cr.r > 0 && (cr.w == len(cr.buf) || len(cr.buf)-cr.w < 512) {
			cr.compact()
		}

		resp.headerRaw = resp.headerRaw[:0]
		connClose, chunked, cl, clSet, err := readClientHeaders(cr, &resp.headerRaw, maxHdrBytes, maxHeaders)
		if err != nil {
			return err
		}
		resp.connClose = connClose
		// Headers are owned by resp now; unpin so body reads can compact.
		cr.release()

		if code >= 100 && code < 200 {
			n1xx++
			if n1xx > maxInformational {
				return ErrBadRequest
			}
			// Informational: discard and read the next response.
			resp.headerRaw = resp.headerRaw[:0]
			continue
		}

		// No body for HEAD, 204, 304 (RFC 9110).
		noBody := code == 204 || code == 304 || method == "HEAD"
		if noBody {
			resp.body = resp.body[:0]
			cr.release()
			clientMaybeCompact(cr)
			return nil
		}

		if streamBody {
			if chunked {
				rs := acquireRequestStream(cr, -1, maxBody, maxHeaders, maxChunks, true)
				resp.bodyStream = &ownedBodyStream{rs: rs, hc: hc, cc: cc, closeConn: connClose}
				return nil
			}
			if clSet {
				if cl > maxBody {
					return ErrBodyTooLarge
				}
				if cl > 0 {
					rs := acquireRequestStream(cr, cl, maxBody, maxHeaders, maxChunks, false)
					resp.bodyStream = &ownedBodyStream{rs: rs, hc: hc, cc: cc, closeConn: connClose}
					return nil
				}
				resp.body = resp.body[:0]
				cr.release()
				clientMaybeCompact(cr)
				return nil
			}
			// No CL and not chunked: connection-close body — stream until EOF then close conn.
			resp.bodyStream = &eofBodyStream{cr: cr, maxSize: maxBody, hc: hc, cc: cc, total: 0}
			resp.connClose = true
			return nil
		}

		var body []byte
		switch {
		case chunked:
			body, err = readClientChunked(cr, resp.body, maxBody, maxChunks)
		case clSet:
			if cl > maxBody {
				return ErrBodyTooLarge
			}
			if cl > 0 {
				body = resp.body
				if cap(body) < cl {
					body = make([]byte, cl)
				} else {
					body = body[:cl]
				}
				if err = cr.readFull(body); err != nil {
					return err
				}
			} else {
				body = resp.body[:0]
			}
		default:
			// No CL and not chunked: read until EOF (connection close).
			body, err = readUntilEOF(cr, resp.body, maxBody)
			resp.connClose = true
		}
		if err != nil {
			return err
		}
		resp.body = body
		cr.release()
		clientMaybeCompact(cr)
		return nil
	}
}

func clientMaybeCompact(cr *connReader) {
	if cr.r > 0 && (cr.r > len(cr.buf)/2 || len(cr.buf)-cr.w < 512) {
		cr.compact()
	}
}

func parseStatusLine(line []byte) (int, bool) {
	// HTTP/1.x NNN ...
	if len(line) < 12 {
		return 0, false
	}
	if line[0] != 'H' || line[1] != 'T' || line[2] != 'T' || line[3] != 'P' || line[4] != '/' {
		return 0, false
	}
	sp := -1
	for i := 5; i < len(line); i++ {
		if line[i] == ' ' {
			sp = i
			break
		}
	}
	if sp < 0 || sp+3 >= len(line) {
		return 0, false
	}
	code := 0
	for i := 0; i < 3; i++ {
		c := line[sp+1+i]
		if c < '0' || c > '9' {
			return 0, false
		}
		code = code*10 + int(c-'0')
	}
	return code, true
}

func readClientHeaders(cr *connReader, hdr *[]byte, maxBytes, maxHeaders int) (connClose, chunked bool, cl int, clSet bool, err error) {
	if maxBytes <= 0 {
		maxBytes = 1 << 20
	}
	if maxHeaders <= 0 {
		maxHeaders = defaultMaxHeaders
	}
	start := cr.off
	nHeaders := 0
	for {
		line, e := cr.readLine()
		if e != nil {
			err = e
			return
		}
		if cr.off-start > maxBytes {
			err = ErrHeaderTooLarge
			return
		}
		if len(line) == 0 {
			if chunked && clSet {
				err = errChunkedConflict
				return
			}
			// One-shot copy of the header block (excludes the blank CRLF).
			end := cr.off
			if end >= start+2 {
				end -= 2
			}
			*hdr = append((*hdr)[:0], cr.buf[start:end]...)
			return
		}
		nHeaders++
		if nHeaders > maxHeaders {
			err = ErrBadRequest
			return
		}
		colon := -1
		for i, c := range line {
			if c == ':' {
				colon = i
				break
			}
		}
		if colon <= 0 || !isHeaderNameToken(line[:colon]) {
			err = ErrBadRequest
			return
		}
		key := line[:colon]
		val := line[colon+1:]
		for len(val) > 0 && (val[0] == ' ' || val[0] == '\t') {
			val = val[1:]
		}
		if !validHeaderValue(val) {
			err = ErrBadRequest
			return
		}

		switch {
		case isContentLength(key):
			if clSet {
				err = errDuplicateCL
				return
			}
			n, ok := parseContentLength(val)
			if !ok {
				err = errInvalidContentLength
				return
			}
			cl = n
			clSet = true
		case isTransferEncoding(key):
			if chunked {
				err = ErrBadRequest
				return
			}
			if !equalFoldBytes(val, vChunked) {
				err = ErrBadRequest
				return
			}
			chunked = true
		case isConnection(key):
			closeH, _, _ := parseConnectionDirectives(val)
			if closeH {
				connClose = true
			}
		}
	}
}

func readClientChunked(cr *connReader, dst []byte, maxBody, maxChunks int) ([]byte, error) {
	buf := dst[:0]
	if cap(buf) < 512 {
		buf = make([]byte, 0, 512)
	}
	if maxChunks <= 0 {
		maxChunks = maxChunksPerBody
	}
	nChunks := 0
	for {
		line, err := cr.readLine()
		if err != nil {
			return nil, err
		}
		size, ok := parseHexSize(line)
		if !ok {
			return nil, errBadChunk
		}
		if size == 0 {
			// Trailers — same rules as the server parser.
			nTrail := 0
			for {
				t, err := cr.readLine()
				if err != nil {
					return nil, err
				}
				if len(t) == 0 {
					return buf, nil
				}
				nTrail++
				if nTrail > defaultMaxHeaders {
					return nil, ErrBadRequest
				}
				colon := -1
				for i, c := range t {
					if c == ':' {
						colon = i
						break
					}
				}
				if colon <= 0 || !isHeaderNameToken(t[:colon]) {
					return nil, ErrBadRequest
				}
				key := t[:colon]
				if forbiddenTrailer(key) {
					return nil, ErrBadRequest
				}
				val := t[colon+1:]
				for len(val) > 0 && (val[0] == ' ' || val[0] == '\t') {
					val = val[1:]
				}
				if !validHeaderValue(val) {
					return nil, ErrBadRequest
				}
			}
		}
		nChunks++
		if nChunks > maxChunks {
			return nil, ErrBadRequest
		}
		if len(buf)+size > maxBody {
			return nil, ErrBodyTooLarge
		}
		need := size
		for need > 0 {
			avail := cr.w - cr.off
			if avail == 0 {
				if err := cr.fill(); err != nil {
					return nil, err
				}
				avail = cr.w - cr.off
			}
			take := avail
			if take > need {
				take = need
			}
			buf = append(buf, cr.buf[cr.off:cr.off+take]...)
			cr.off += take
			need -= take
		}
		if err := consumeCRLF(cr); err != nil {
			return nil, err
		}
	}
}

func readUntilEOF(cr *connReader, dst []byte, maxBody int) ([]byte, error) {
	buf := dst[:0]
	if cap(buf) < 512 {
		buf = make([]byte, 0, 512)
	}
	for {
		avail := cr.w - cr.off
		if avail > 0 {
			if len(buf)+avail > maxBody {
				return nil, ErrBodyTooLarge
			}
			buf = append(buf, cr.buf[cr.off:cr.w]...)
			cr.off = cr.w
			continue
		}
		err := cr.fill()
		avail = cr.w - cr.off
		if avail > 0 {
			if len(buf)+avail > maxBody {
				return nil, ErrBodyTooLarge
			}
			buf = append(buf, cr.buf[cr.off:cr.w]...)
			cr.off = cr.w
		}
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return buf, nil
			}
			return nil, err
		}
	}
}

// CloseIdleConnections closes idle keep-alive connections.
func (hc *HostClient) CloseIdleConnections() {
	hc.init()
	if cc := hc.sticky.Swap(nil); cc != nil {
		_ = cc.raw.Close()
		cc.cr.releaseBuf()
		hc.mu.Lock()
		hc.conns--
		hc.mu.Unlock()
	}
	hc.mu.Lock()
	idle := hc.idle
	hc.idle = nil
	hc.conns -= len(idle)
	hc.mu.Unlock()
	for _, cc := range idle {
		_ = cc.raw.Close()
		cc.cr.releaseBuf()
	}
}

// CloseIdleConnections closes idle connections across all HostClients.
func (c *Client) CloseIdleConnections() {
	c.mu.Lock()
	hosts := make([]*HostClient, 0, len(c.hosts))
	for _, hc := range c.hosts {
		hosts = append(hosts, hc)
	}
	c.mu.Unlock()
	for _, hc := range hosts {
		hc.CloseIdleConnections()
	}
}

// String returns a debug description.
func (hc *HostClient) String() string {
	scheme := "http"
	if hc.IsTLS {
		scheme = "https"
	}
	return fmt.Sprintf("HostClient{%s://%s}", scheme, hc.Addr)
}
