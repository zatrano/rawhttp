package rawhttp

import (
	"context"
	"crypto/tls"
	"errors"
	"log"
	"net"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Server is an HTTP/1.1 server.
type Server struct {
	// Handler is called for each request.
	Handler Handler

	// Name, when non-empty, is sent as the Server response header.
	Name string

	// Network is passed to net.Listen for ListenAndServe / ListenAndServeTLS.
	// Empty → "tcp". Use "tcp4", "tcp6", or "unix" as needed.
	Network string

	// ReadTimeout is the maximum duration for reading the entire request
	// including body. Zero uses 30s; negative disables the deadline.
	ReadTimeout time.Duration

	// WriteTimeout is the maximum duration before timing out writes.
	// Zero uses 30s; negative disables the deadline.
	WriteTimeout time.Duration

	// IdleTimeout is the maximum time to wait for the next request when
	// keep-alive is enabled. Zero uses 90s; negative disables.
	// If only IdleTimeout is negative and ReadTimeout is set, idle waits forever.
	IdleTimeout time.Duration

	// MaxRequestBodySize rejects bodies larger than this. Zero uses 4MiB.
	MaxRequestBodySize int

	// MaxHeaderBytes is the maximum size of the request header block.
	// Zero uses 8192. Oversized blocks → 431.
	MaxHeaderBytes int
	// ReadBufferSize is the per-connection read buffer. Zero → MaxHeaderBytes
	// (or 8192). May exceed MaxHeaderBytes for keep-alive locality; the
	// header-block ceiling remains MaxHeaderBytes.
	ReadBufferSize int
	// WriteBufferSize, when > 0, enables a buffered writer for response
	// streaming paths. Zero keeps direct writes (fastest for small bodies).
	WriteBufferSize int

	// StreamRequestBody, when true, does not buffer the full request body
	// before calling Handler. Read via Ctx.RequestBodyStream. Unread bytes
	// are drained after the handler so keep-alive stays correct.
	// Default false — zero cost on the plaintext hot path.
	StreamRequestBody bool

	// DisablePathNormalizing, when true, allows ".." path segments in the
	// request target (proxy / raw forwarding). Default rejects them.
	DisablePathNormalizing bool

	// MaxHeaders limits the number of request header lines. Zero uses 100.
	MaxHeaders int

	// MaxChunks limits non-zero chunk frames in a chunked body. Zero uses 16384.
	MaxChunks int

	// ReadHeaderTimeout is the amount of time allowed to read request headers
	// after the request line. Zero uses ReadTimeout; negative disables.
	ReadHeaderTimeout time.Duration

	// Concurrency limits simultaneous connections. Zero uses 262144;
	// negative means unlimited.
	Concurrency int

	// DisableKeepalive closes the connection after each response.
	DisableKeepalive bool

	// DisablePipelining closes the connection when a second request is already
	// buffered after writing a response (HTTP/1.1 pipelining). Keep-alive without
	// pipelining is unchanged. Useful when fronting untrusted clients.
	DisablePipelining bool

	// DisablePanicRecovery skips the per-request recover() wrapper. Default false
	// (safe). Enable only for trusted handlers / microbenchmarks.
	DisablePanicRecovery bool

	// DisableRequestStats skips TotalRequests increments on the serve hot path.
	DisableRequestStats bool

	// CloseOnShutdown is accepted for API compatibility. rawhttp always
	// forces Connection: close once Shutdown begins so Wait returns promptly
	// (idle conns are also closed in Shutdown).
	CloseOnShutdown bool

	// GetOnly rejects non-GET methods with 405 before the handler runs.
	GetOnly bool

	// AllowedMethods, when non-empty, rejects other methods with 405 and an
	// Allow header listing these methods. Takes precedence over GetOnly when set.
	AllowedMethods []string

	// RequireTLS rejects cleartext requests with 403 when the connection is
	// not TLS (useful behind optional TLS or ServeConn misuse).
	RequireTLS bool

	// AllowedHosts, when non-empty, requires the request Host to match one
	// entry (case-insensitive; port optional / ignored for match). Else 421.
	AllowedHosts []string

	// MaxURILength rejects request-targets longer than this (path + optional
	// "?" + query). Zero means unlimited.
	MaxURILength int

	// TrustedProxies is a list of CIDR or IP prefixes. When non-empty,
	// Ctx.ClientIP() may honor X-Forwarded-For / X-Real-IP from those peers.
	// Empty → ClientIP equals RemoteIP (never trust forwarded headers).
	TrustedProxies []string

	// NoDefaultDate omits the Date response header. By default rawhttp sends
	// a coarse (1s) Date header without a per-request time.Now.
	NoDefaultDate bool

	// NoDefaultContentType omits the default Content-Type: text/plain on 200
	// responses when the handler did not set one.
	NoDefaultContentType bool

	// TCPKeepalive enables TCP keep-alive on accepted connections.
	TCPKeepalive bool
	// TCPKeepalivePeriod sets the TCP keep-alive period when TCPKeepalive is true.
	// Zero leaves the OS default.
	TCPKeepalivePeriod time.Duration

	// MaxRequestsPerConn limits keep-alive requests on one connection.
	// Zero means unlimited.
	MaxRequestsPerConn int

	// MaxConnDuration limits how long a keep-alive connection may live
	// from accept/ServeConn start. Zero means unlimited.
	MaxConnDuration time.Duration

	// ContinueTimeout is the max time to read the request body after sending
	// 100 Continue. Zero uses ReadTimeout for the body phase.
	ContinueTimeout time.Duration

	// ContinueHandler, when set, is called for Expect: 100-continue before
	// sending 100 Continue. Return false to reject with 417 and close.
	ContinueHandler func(ctx *Ctx) bool

	// HeaderReceived, when set, is called after request headers are parsed.
	// Non-zero fields in the returned RequestConfig override server defaults
	// for this request only (body size / read deadline before body).
	HeaderReceived func(ctx *Ctx) RequestConfig

	// MaxConnsPerIP limits concurrent connections from one client IP.
	// Zero means unlimited. Excess accepts get 429 and are closed.
	MaxConnsPerIP int

	// ConcurrencyWaitTimeout is how long Serve waits for a concurrency slot.
	// Zero waits forever. Positive → close the accepted connection on timeout.
	ConcurrencyWaitTimeout time.Duration

	// MaxMultipartMemory caps in-memory multipart parts (mime/multipart).
	// Zero → 16MiB. Used when MultipartForm(0) is called.
	MaxMultipartMemory int64
	// MaxMultipartFiles caps uploaded files per multipart form. Zero → 32.
	MaxMultipartFiles int
	// MaxMultipartParts caps total parts (fields+files). Zero → 128.
	MaxMultipartParts int

	// ConnState is called when a connection changes state. Nil disables.
	ConnState func(conn net.Conn, state ConnState)

	// ErrorCallback is invoked for protocol/client errors after the error
	// response is written (if any). Nil means no callback.
	// Prefer ErrorHandler when both are set.
	ErrorCallback func(err error)

	// ErrorHandler is like ErrorCallback but receives the request Ctx when
	// available (may be nil for accept/listen errors).
	ErrorHandler func(ctx *Ctx, err error)

	// FormValueFunc customizes Ctx.FormValue lookup order. nil → query, then
	// post form, then multipart. Use NetHTTPFormValueFunc for net/http parity
	// (body before query).
	FormValueFunc FormValueFunc

	// KeepHijackedConns, when true, does not Close hijacked connections after
	// the handler returns (caller owns lifecycle). Default false matches
	// the accept loop closes the conn when the serve goroutine ends.
	KeepHijackedConns bool

	// AllowUpgrade, when true, admits a standards-shaped WebSocket handshake
	// (GET HTTP/1.1, Connection upgrade token, Upgrade: websocket, key/version)
	// to the Handler so it can Hijack. Default false rejects Upgrade /
	// Connection: upgrade with 400 before the handler (safe default).
	AllowUpgrade bool

	// ReduceMemoryUsage, when true, drops request body buffers after each
	// request so large uploads do not retain capacity across keep-alive.
	ReduceMemoryUsage bool

	// ErrorLog logs panics and serve errors. Defaults to log.Default().
	ErrorLog *log.Logger

	// OpenConnections is the number of active connections.
	OpenConnections atomic.Int64
	// TotalConnections is the number of accepted connections.
	TotalConnections atomic.Int64
	// TotalRequests is the number of requests handled (including bad requests
	// that produced a response).
	TotalRequests atomic.Int64

	mu            sync.Mutex
	listeners     map[net.Listener]struct{}
	activeConn    sync.WaitGroup
	conns         map[*connState]struct{}
	shutting      atomic.Bool
	concurrencyCh chan struct{}
	trustedOnce   sync.Once
	trustedNets   []*net.IPNet
	perIP         perIPCounter
}

// RequestConfig overrides per-request limits from HeaderReceived.
type RequestConfig struct {
	// ReadTimeout overrides the body-phase read deadline. Zero keeps server default.
	// Negative disables the deadline for this request's body phase.
	ReadTimeout time.Duration
	// MaxRequestBodySize overrides the body size cap. Zero keeps server default.
	MaxRequestBodySize int
}

// BodyLimitConfig returns a HeaderReceived helper that caps MaxRequestBodySize at n.
// When n <= 0 the returned config is empty (no override).
func BodyLimitConfig(n int) func(*Ctx) RequestConfig {
	return func(*Ctx) RequestConfig {
		if n <= 0 {
			return RequestConfig{}
		}
		return RequestConfig{MaxRequestBodySize: n}
	}
}

type connState struct {
	conn     net.Conn
	idle     atomic.Bool
	hijacked atomic.Bool
}

// ConnState represents the lifecycle of a connection for Server.ConnState.
type ConnState int

const (
	// StateNew is a newly accepted connection before the first request.
	StateNew ConnState = iota
	// StateActive means a request is being read or handled.
	StateActive
	// StateIdle means keep-alive wait for the next request.
	StateIdle
	// StateClosed means the connection is closed.
	StateClosed
)

func (s *Server) reportConnState(conn net.Conn, state ConnState) {
	if s.ConnState != nil {
		s.ConnState(conn, state)
	}
}

func (s *Server) maxBody() int {
	if s.MaxRequestBodySize > 0 {
		return s.MaxRequestBodySize
	}
	return defaultMaxBodySize
}

func (s *Server) headerBufSize() int {
	if s.MaxHeaderBytes > 0 {
		return s.MaxHeaderBytes
	}
	return defaultMaxHeaderBytes
}

func (s *Server) connReadBufSize() int {
	hdrLim := s.headerBufSize()
	if s.ReadBufferSize > 0 {
		n := s.ReadBufferSize
		if n < 128 {
			n = 128
		}
		if n < hdrLim {
			return hdrLim
		}
		return n
	}
	return hdrLim
}

func (s *Server) maxHeaders() int {
	if s.MaxHeaders > 0 {
		return s.MaxHeaders
	}
	return defaultMaxHeaders
}

func (s *Server) maxChunks() int {
	if s.MaxChunks > 0 {
		return s.MaxChunks
	}
	return maxChunksPerBody
}

func (s *Server) logger() *log.Logger {
	if s.ErrorLog != nil {
		return s.ErrorLog
	}
	return log.Default()
}

func (s *Server) concurrencyLimit() int {
	if s.Concurrency < 0 {
		return 0
	}
	if s.Concurrency == 0 {
		return defaultConcurrency
	}
	return s.Concurrency
}

func (s *Server) readTimeout() time.Duration {
	if s.ReadTimeout < 0 {
		return 0
	}
	if s.ReadTimeout == 0 {
		return defaultReadTimeout
	}
	return s.ReadTimeout
}

func (s *Server) writeTimeout() time.Duration {
	if s.WriteTimeout < 0 {
		return 0
	}
	if s.WriteTimeout == 0 {
		return defaultWriteTimeout
	}
	return s.WriteTimeout
}

func (s *Server) headerTimeout() time.Duration {
	if s.ReadHeaderTimeout < 0 {
		return 0
	}
	if s.ReadHeaderTimeout == 0 {
		return s.readTimeout()
	}
	return s.ReadHeaderTimeout
}

func (s *Server) idleTimeout() time.Duration {
	if s.IdleTimeout < 0 {
		return 0
	}
	if s.IdleTimeout == 0 {
		return defaultIdleTimeout
	}
	return s.IdleTimeout
}

func (s *Server) initConcurrency() {
	s.mu.Lock()
	defer s.mu.Unlock()
	lim := s.concurrencyLimit()
	if lim > 0 && s.concurrencyCh == nil {
		s.concurrencyCh = make(chan struct{}, lim)
	}
	if s.listeners == nil {
		s.listeners = make(map[net.Listener]struct{})
	}
	if s.conns == nil {
		s.conns = make(map[*connState]struct{})
	}
}

// ListenAndServe listens on Network+addr and serves HTTP.
func (s *Server) ListenAndServe(addr string) error {
	ln, err := net.Listen(s.listenNetwork(), addr)
	if err != nil {
		return err
	}
	return s.Serve(ln)
}

// ListenAndServeTLS listens on Network+addr and serves HTTPS (TLS 1.2+).
func (s *Server) ListenAndServeTLS(addr, certFile, keyFile string) error {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return err
	}
	cfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}
	ln, err := net.Listen(s.listenNetwork(), addr)
	if err != nil {
		return err
	}
	return s.Serve(tls.NewListener(ln, cfg))
}

func (s *Server) listenNetwork() string {
	if s.Network != "" {
		return s.Network
	}
	return "tcp"
}

// Serve accepts connections from ln until closed or Shutdown.
func (s *Server) Serve(ln net.Listener) error {
	s.initConcurrency()
	s.mu.Lock()
	s.listeners[ln] = struct{}{}
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.listeners, ln)
		s.mu.Unlock()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if s.shutting.Load() {
				return ErrServerClosed
			}
			return err
		}
		s.configureAcceptedConn(conn)
		s.reportConnState(conn, StateNew)
		peerIP := ""
		if s.MaxConnsPerIP > 0 {
			peerIP = connRemoteIP(conn)
			if !s.perIP.tryAcquire(peerIP, s.MaxConnsPerIP) {
				writeTooManyRequests(conn)
				_ = conn.Close()
				continue
			}
		}
		if s.concurrencyCh != nil {
			if !s.acquireConcurrency() {
				if peerIP != "" {
					s.perIP.release(peerIP)
				}
				_ = conn.Close()
				continue
			}
		}
		cs := &connState{conn: conn}
		cs.idle.Store(true)
		s.trackConn(cs, true)
		s.activeConn.Add(1)
		s.TotalConnections.Add(1)
		s.OpenConnections.Add(1)
		go func() {
			defer func() {
				s.reportConnState(conn, StateClosed)
				s.OpenConnections.Add(-1)
				s.trackConn(cs, false)
				s.activeConn.Done()
				if !s.KeepHijackedConns || !cs.hijacked.Load() {
					_ = conn.Close()
				}
				if peerIP != "" {
					s.perIP.release(peerIP)
				}
				if s.concurrencyCh != nil {
					<-s.concurrencyCh
				}
			}()
			_ = s.serveTracked(cs)
		}()
	}
}

func (s *Server) trustedProxyNets() []*net.IPNet {
	s.trustedOnce.Do(func() {
		s.trustedNets = parseTrustedProxies(s.TrustedProxies)
	})
	return s.trustedNets
}

func (s *Server) acquireConcurrency() bool {
	if s.ConcurrencyWaitTimeout <= 0 {
		s.concurrencyCh <- struct{}{}
		return true
	}
	timer := time.NewTimer(s.ConcurrencyWaitTimeout)
	select {
	case s.concurrencyCh <- struct{}{}:
		if !timer.Stop() {
			<-timer.C
		}
		return true
	case <-timer.C:
		return false
	}
}

func hostAllowed(host []byte, allowed []string) bool {
	h := string(host)
	// Strip optional port for comparison.
	if i := strings.LastIndexByte(h, ':'); i > 0 {
		// Ignore IPv6 bracket form for simple host:port.
		if h[0] != '[' {
			h = h[:i]
		}
	}
	// Trailing DNS dot is insignificant for allowlist matching.
	h = strings.TrimSuffix(h, ".")
	for _, a := range allowed {
		if a == "" {
			continue
		}
		aa := a
		if j := strings.LastIndexByte(aa, ':'); j > 0 && aa[0] != '[' {
			aa = aa[:j]
		}
		aa = strings.TrimSuffix(aa, ".")
		if strings.EqualFold(h, aa) {
			return true
		}
	}
	return false
}

func methodAllowed(method []byte, allowed []string) bool {
	for _, a := range allowed {
		if equalFoldStr(method, a) {
			return true
		}
	}
	return false
}

func (s *Server) configureAcceptedConn(conn net.Conn) {
	if !s.TCPKeepalive {
		return
	}
	tc, ok := conn.(*net.TCPConn)
	if !ok {
		return
	}
	_ = tc.SetKeepAlive(true)
	if s.TCPKeepalivePeriod > 0 {
		_ = tc.SetKeepAlivePeriod(s.TCPKeepalivePeriod)
	}
}

func (s *Server) trackConn(cs *connState, add bool) {
	s.mu.Lock()
	if add {
		s.conns[cs] = struct{}{}
	} else {
		delete(s.conns, cs)
	}
	s.mu.Unlock()
}

// Shutdown gracefully stops the server: closes listeners, idle connections,
// then waits for active handlers (or ctx cancellation, which force-closes).
func (s *Server) Shutdown(ctx context.Context) error {
	s.shutting.Store(true)

	s.mu.Lock()
	lns := make([]net.Listener, 0, len(s.listeners))
	for ln := range s.listeners {
		lns = append(lns, ln)
	}
	idles := make([]net.Conn, 0)
	for cs := range s.conns {
		if cs.idle.Load() {
			idles = append(idles, cs.conn)
		}
	}
	s.mu.Unlock()

	for _, ln := range lns {
		_ = ln.Close()
	}
	for _, c := range idles {
		_ = c.Close()
	}

	done := make(chan struct{})
	go func() {
		s.activeConn.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		s.mu.Lock()
		for cs := range s.conns {
			_ = cs.conn.Close()
		}
		s.mu.Unlock()
		<-done
		return ctx.Err()
	}
}

// Close immediately stops the server and closes all listeners and connections.
func (s *Server) Close() error {
	s.shutting.Store(true)

	s.mu.Lock()
	lns := make([]net.Listener, 0, len(s.listeners))
	for ln := range s.listeners {
		lns = append(lns, ln)
	}
	conns := make([]net.Conn, 0, len(s.conns))
	for cs := range s.conns {
		conns = append(conns, cs.conn)
	}
	s.mu.Unlock()

	for _, ln := range lns {
		_ = ln.Close()
	}
	for _, c := range conns {
		_ = c.Close()
	}
	s.activeConn.Wait()
	return nil
}

// ServeConn handles one connection until close or error.
// Does not close conn — caller owns the lifecycle.
func (s *Server) ServeConn(conn net.Conn) error {
	if s.MaxConnsPerIP > 0 {
		ip := connRemoteIP(conn)
		if !s.perIP.tryAcquire(ip, s.MaxConnsPerIP) {
			writeTooManyRequests(conn)
			return ErrPerIPConnLimit
		}
		defer s.perIP.release(ip)
	}
	return s.serveLoop(conn, nil)
}

func (s *Server) serveTracked(cs *connState) error {
	return s.serveLoop(cs.conn, cs)
}

func (s *Server) serveLoop(conn net.Conn, cs *connState) error {
	maxHdrBytes := s.headerBufSize()
	bufSize := s.connReadBufSize()
	if bufSize < 128 {
		bufSize = 128
	}
	cr := newConnReader(conn, bufSize)
	defer cr.releaseBuf()
	ctx := ctxPool.Get().(*Ctx)
	defer ctxPool.Put(ctx)
	handler := s.Handler
	if handler == nil {
		handler = func(ctx *Ctx) { ctx.NotFound() }
	}
	maxBody := s.maxBody()
	maxHdrs := s.maxHeaders()
	maxChk := s.maxChunks()
	disableKA := s.DisableKeepalive
	skipDL := s.noDeadlines()
	maxReq := s.MaxRequestsPerConn
	maxDur := s.MaxConnDuration
	connStart := time.Now()
	connID := nextConnID()
	var reqNum uint64
	remote := ""
	local := ""
	if ra := conn.RemoteAddr(); ra != nil {
		remote = ra.String()
	}
	if la := conn.LocalAddr(); la != nil {
		local = la.String()
	}
	_, isTLS := conn.(*tls.Conn)

	noDefaultDate := s.NoDefaultDate
	noDefaultCT := s.NoDefaultContentType
	writeBufSize := s.WriteBufferSize
	streamReqBody := s.StreamRequestBody
	reduceMem := s.ReduceMemoryUsage
	allowDotDot := s.DisablePathNormalizing
	serverName := ""
	if s.Name != "" && validHeaderValue([]byte(s.Name)) {
		serverName = s.Name
	}
	maxMPMem := s.MaxMultipartMemory
	maxMPFiles := s.MaxMultipartFiles
	maxMPParts := s.MaxMultipartParts
	formValueFunc := s.FormValueFunc
	var trustedProxies []*net.IPNet
	if len(s.TrustedProxies) > 0 {
		trustedProxies = s.trustedProxyNets()
	}

	for {
		ctx.reset()
		ctx.remoteAddr = remote
		ctx.localAddr = local
		ctx.isTLS = isTLS
		ctx.noDefaultDate = noDefaultDate
		ctx.noDefaultCT = noDefaultCT
		ctx.serverName = serverName
		ctx.writeBufSize = writeBufSize
		ctx.maxMultipartMemory = maxMPMem
		ctx.maxMultipartFiles = maxMPFiles
		ctx.maxMultipartParts = maxMPParts
		ctx.formValueFunc = formValueFunc
		ctx.trustedProxies = trustedProxies
		ctx.conn = conn
		ctx.cr = cr
		ctx.connID = connID
		ctx.connTime = connStart
		reqNum++
		ctx.connRequestNum = reqNum
		if cs != nil && !s.DisableRequestStats {
			s.TotalRequests.Add(1)
		}

		if cs != nil {
			cs.idle.Store(true)
		}
		if s.ConnState != nil {
			s.reportConnState(conn, StateIdle)
		}
		if !skipDL {
			// Idle only while waiting for the first byte of the next request.
			// Once any data is buffered (or arrives), switch to header deadline
			// so a dripped request-line cannot burn the full IdleTimeout.
			if cr.w == cr.off {
				if err := s.setIdleDeadline(conn); err != nil {
					return err
				}
				for cr.w == cr.off {
					if err := cr.fill(); err != nil {
						return err
					}
				}
			}
			if err := s.setHeaderDeadline(conn); err != nil {
				return err
			}
		}

		if cr.r > 0 && len(cr.buf)-cr.r < 2048 {
			cr.compact()
		}

		if err := parseRequestLineOpts(cr, ctx, allowDotDot); err != nil {
			return s.clientError(conn, err)
		}
		if maxURI := s.MaxURILength; maxURI > 0 {
			n := len(ctx.Path)
			if len(ctx.Query) > 0 {
				n += 1 + len(ctx.Query)
			}
			if n > maxURI {
				return s.clientError(conn, ErrURITooLong)
			}
		}

		if cs != nil {
			cs.idle.Store(false)
		}
		if s.ConnState != nil {
			s.reportConnState(conn, StateActive)
		}
		if !skipDL {
			if err := s.setHeaderDeadline(conn); err != nil {
				return err
			}
		}

		if err := parseHeaders(cr, ctx, maxHdrs, maxHdrBytes, s.AllowUpgrade); err != nil {
			return s.clientError(conn, err)
		}
		if s.AllowUpgrade && (ctx.upgradeWanted || len(ctx.upgradeProto) > 0) {
			if err := validateWebSocketUpgrade(ctx); err != nil {
				return s.clientError(conn, err)
			}
		}

		reqMaxBody := maxBody
		var bodyReadTO time.Duration // 0 → server default; negative → unlimited
		var bodyReadTOSet bool
		if s.HeaderReceived != nil {
			cfg := s.HeaderReceived(ctx)
			if cfg.MaxRequestBodySize > 0 {
				reqMaxBody = cfg.MaxRequestBodySize
			}
			if cfg.ReadTimeout != 0 {
				bodyReadTO = cfg.ReadTimeout
				bodyReadTOSet = true
			}
		}

		if len(s.AllowedHosts) > 0 && !hostAllowed(ctx.host, s.AllowedHosts) {
			return s.clientError(conn, ErrMisdirectedRequest)
		}

		if s.RequireTLS && !ctx.isTLS {
			if !skipDL {
				if err := s.setWriteDeadline(conn); err != nil {
					return err
				}
			}
			ctx.Forbidden()
			cr.release()
			_ = writeResponse(conn, ctx, true)
			return nil
		}

		if len(s.AllowedMethods) > 0 {
			if !methodAllowed(ctx.Method, s.AllowedMethods) {
				if !skipDL {
					if err := s.setWriteDeadline(conn); err != nil {
						return err
					}
				}
				_ = ctx.MethodNotAllowed(strings.Join(s.AllowedMethods, ", "))
				cr.release()
				_ = writeResponse(conn, ctx, true)
				return nil
			}
		} else if s.GetOnly && !isGetMethod(ctx.Method) {
			if !skipDL {
				if err := s.setWriteDeadline(conn); err != nil {
					return err
				}
			}
			_ = ctx.MethodNotAllowed("GET")
			cr.release()
			_ = writeResponse(conn, ctx, true)
			return nil
		}

		if ctx.expectContinue && s.ContinueHandler != nil && !s.ContinueHandler(ctx) {
			if !skipDL {
				if err := s.setWriteDeadline(conn); err != nil {
					return err
				}
			}
			writeExpectationFailed(conn)
			return errExpectationFailed
		}

		needBody := (ctx.clSet && ctx.contentLength > 0) || ctx.chunked
		bodyPinned := false // Content-Length body still aliases the read buffer
		if needBody {
			if ctx.expectContinue {
				if !skipDL {
					if err := s.setWriteDeadline(conn); err != nil {
						return err
					}
				}
				if err := writeContinue(conn); err != nil {
					return err
				}
			}
			if !skipDL {
				if ctx.expectContinue && s.ContinueTimeout > 0 {
					if err := conn.SetReadDeadline(time.Now().Add(s.ContinueTimeout)); err != nil {
						return err
					}
				} else if bodyReadTOSet {
					if bodyReadTO < 0 {
						if err := conn.SetReadDeadline(time.Time{}); err != nil {
							return err
						}
					} else if err := conn.SetReadDeadline(time.Now().Add(bodyReadTO)); err != nil {
						return err
					}
				} else if err := s.setReadDeadline(conn); err != nil {
					return err
				}
			}

			// Hot path: small Content-Length body already (or soon) in the read
			// buffer — keep header slices pinned, skip ownership copies.
			if !streamReqBody && !ctx.chunked && ctx.clSet && ctx.contentLength > 0 {
				n := ctx.contentLength
				if n > reqMaxBody {
					if !skipDL {
						_ = s.setWriteDeadline(conn)
					}
					writeEntityTooLarge(conn)
					return ErrBodyTooLarge
				}
				if body, ok := cr.takeBufferedBody(n); ok {
					// Copy body into owned buffer so response keep-alive cache
					// can key on a stable &reqBody[0]; headers stay pinned.
					ctx.reqBody = append(ctx.reqBody[:0], body...)
					bodyPinned = true
				}
			}

			if !bodyPinned {
				ctx.hdrCopy = append(ctx.hdrCopy[:0], ctx.headerBlock...)
				ctx.headerBlock = ctx.hdrCopy
				ctx.methodCopy = append(ctx.methodCopy[:0], ctx.Method...)
				ctx.Method = ctx.methodCopy
				ctx.pathCopy = append(ctx.pathCopy[:0], ctx.Path...)
				ctx.Path = ctx.pathCopy
				if len(ctx.Query) > 0 {
					ctx.queryCopy = append(ctx.queryCopy[:0], ctx.Query...)
					ctx.Query = ctx.queryCopy
				}
				if len(ctx.host) > 0 {
					ctx.hostCopy = append(ctx.hostCopy[:0], ctx.host...)
					ctx.host = ctx.hostCopy
				}
				if len(ctx.reqContentType) > 0 {
					ctx.ctCopy = append(ctx.ctCopy[:0], ctx.reqContentType...)
					ctx.reqContentType = ctx.ctCopy
				}
				if len(ctx.userAgent) > 0 {
					ctx.uaCopy = append(ctx.uaCopy[:0], ctx.userAgent...)
					ctx.userAgent = ctx.uaCopy
				}
				if len(ctx.accept) > 0 {
					ctx.acceptCopy = append(ctx.acceptCopy[:0], ctx.accept...)
					ctx.accept = ctx.acceptCopy
				}
				if len(ctx.acceptEncoding) > 0 {
					ctx.acceptEncCopy = append(ctx.acceptEncCopy[:0], ctx.acceptEncoding...)
					ctx.acceptEncoding = ctx.acceptEncCopy
				}
				if len(ctx.cookieHdr) > 0 {
					ctx.cookieCopy = append(ctx.cookieCopy[:0], ctx.cookieHdr...)
					ctx.cookieHdr = ctx.cookieCopy
				}
				if len(ctx.referer) > 0 {
					ctx.refererCopy = append(ctx.refererCopy[:0], ctx.referer...)
					ctx.referer = ctx.refererCopy
				}
				if len(ctx.authorization) > 0 {
					ctx.authCopy = append(ctx.authCopy[:0], ctx.authorization...)
					ctx.authorization = ctx.authCopy
				}
				if len(ctx.origin) > 0 {
					ctx.originCopy = append(ctx.originCopy[:0], ctx.origin...)
					ctx.origin = ctx.originCopy
				}
				// Extra header slices pointed into the live buffer; drop them —
				// Header() falls back to scanning the owned headerBlock copy.
				ctx.extraKeys = ctx.extraKeys[:0]
				ctx.extraVals = ctx.extraVals[:0]
				cr.release()

				if streamReqBody {
					if err := prepareRequestBodyStream(cr, ctx, reqMaxBody, maxHdrs, maxChk); err != nil {
						if errors.Is(err, ErrBodyTooLarge) {
							if !skipDL {
								_ = s.setWriteDeadline(conn)
							}
							writeEntityTooLarge(conn)
							return err
						}
						return s.clientError(conn, err)
					}
				} else if err := readRequestBody(cr, ctx, reqMaxBody, maxHdrs, maxChk); err != nil {
					if errors.Is(err, ErrBodyTooLarge) {
						if !skipDL {
							_ = s.setWriteDeadline(conn)
						}
						writeEntityTooLarge(conn)
						return err
					}
					return s.clientError(conn, err)
				}
			}
		}

		if !skipDL {
			if err := s.setWriteDeadline(conn); err != nil {
				return err
			}
		}

		s.callHandler(handler, ctx)
		if ctx.hijacked {
			// Ownership transferred; Accept defer closes unless KeepHijackedConns.
			if cs != nil {
				cs.hijacked.Store(true)
			}
			return nil
		}
		if streamReqBody && ctx.reqStream != nil {
			drainErr := ctx.reqStream.drain()
			releaseRequestStream(ctx.reqStream)
			ctx.reqStream = nil
			if drainErr != nil {
				return drainErr
			}
		}
		if poisonEnabled {
			poisonPinnedBuffer(cr)
		}
		cr.release()
		// Keep remaining bytes at front so the next request has a contiguous buffer.
		if cr.r > len(cr.buf)/2 {
			cr.compact()
		}

		closeConn := ctx.shouldClose(disableKA) || s.shutting.Load()
		// Admitted Upgrade handshakes that were not Hijack()'d must not keep-alive:
		// leftover bytes are not a next HTTP request.
		if ctx.upgradeWanted {
			closeConn = true
		}
		if maxReq > 0 && reqNum >= uint64(maxReq) {
			closeConn = true
		}
		if maxDur > 0 && time.Since(connStart) >= maxDur {
			closeConn = true
		}
		// Opt-in: refuse HTTP pipelining — close if another request is already buffered.
		if s.DisablePipelining && cr.w > cr.off {
			closeConn = true
		}
		if err := writeResponse(conn, ctx, closeConn); err != nil {
			return err
		}
		if poisonEnabled {
			poisonCtxRequestSlices(ctx)
		}
		if reduceMem {
			if cap(ctx.reqBody) > 4096 {
				ctx.reqBody = nil
			}
			if cap(ctx.respBuf) > 4096 {
				ctx.respBuf = nil
				ctx.respBody = nil
			}
		}
		if closeConn {
			return nil
		}
	}
}
func (s *Server) clientError(conn net.Conn, err error) error {
	return s.clientErrorCtx(nil, conn, err)
}

func (s *Server) clientErrorCtx(ctx *Ctx, conn net.Conn, err error) error {
	if err == nil {
		return nil
	}
	_ = s.setWriteDeadline(conn)
	switch {
	case errors.Is(err, errExpectationFailed):
		writeExpectationFailed(conn)
	case errors.Is(err, errBufferFull):
		writeHeaderTooLarge(conn)
	case errors.Is(err, ErrURITooLong):
		writeURITooLong(conn)
	case errors.Is(err, ErrMisdirectedRequest):
		writeMisdirectedRequest(conn)
	case isBadRequest(err):
		writeBadRequest(conn)
	}
	if s.ErrorHandler != nil {
		s.ErrorHandler(ctx, err)
	} else if cb := s.ErrorCallback; cb != nil {
		cb(err)
	}
	return err
}

func isBadRequest(err error) bool {
	switch {
	case errors.Is(err, errMalformedRequestLine),
		errors.Is(err, ErrBadRequest),
		errors.Is(err, errMissingHost),
		errors.Is(err, errInvalidContentLength),
		errors.Is(err, errDuplicateCL),
		errors.Is(err, errDuplicateHost),
		errors.Is(err, errChunkedConflict),
		errors.Is(err, errBadChunk):
		return true
	default:
		return false
	}
}

func (s *Server) callHandler(handler Handler, ctx *Ctx) {
	if s.DisablePanicRecovery {
		handler(ctx)
		return
	}
	defer func() {
		if rec := recover(); rec != nil {
			s.logger().Printf("rawhttp: panic serving: %v\n%s", rec, debug.Stack())
			ctx.StatusCode = 500
			ctx.respBody = []byte("Internal Server Error")
			ctx.bodyIsRef = false
			ctx.cacheOK = false
			ctx.respChunked = false
			ctx.respHeaderBuf = ctx.respHeaderBuf[:0]
			ctx.userSetCT = false
		}
	}()
	handler(ctx)
}

var globalConnID atomic.Uint64

func nextConnID() uint64 {
	return globalConnID.Add(1)
}

func (s *Server) noDeadlines() bool {
	return s.readTimeout() <= 0 && s.writeTimeout() <= 0 && s.idleTimeout() <= 0 && s.headerTimeout() <= 0
}

func (s *Server) setIdleDeadline(conn net.Conn) error {
	if s.noDeadlines() {
		return nil
	}
	d := s.idleTimeout()
	if d <= 0 {
		return conn.SetReadDeadline(time.Time{})
	}
	return conn.SetReadDeadline(time.Now().Add(d))
}

func (s *Server) setReadDeadline(conn net.Conn) error {
	if s.noDeadlines() {
		return nil
	}
	d := s.readTimeout()
	if d <= 0 {
		return conn.SetReadDeadline(time.Time{})
	}
	return conn.SetReadDeadline(time.Now().Add(d))
}

func (s *Server) setHeaderDeadline(conn net.Conn) error {
	if s.noDeadlines() {
		return nil
	}
	d := s.headerTimeout()
	if d <= 0 {
		return conn.SetReadDeadline(time.Time{})
	}
	return conn.SetReadDeadline(time.Now().Add(d))
}

func (s *Server) setWriteDeadline(conn net.Conn) error {
	if s.noDeadlines() {
		return nil
	}
	d := s.writeTimeout()
	if d <= 0 {
		return conn.SetWriteDeadline(time.Time{})
	}
	return conn.SetWriteDeadline(time.Now().Add(d))
}
