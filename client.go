package rawhttp

import (
	"crypto/tls"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"
)

var (
	// ErrTimeout is returned when a client operation exceeds its deadline.
	ErrTimeout = errors.New("rawhttp: client timeout")
	// ErrNoFreeConns is returned when MaxConnsPerHost is reached and Dial cannot wait.
	ErrNoFreeConns = errors.New("rawhttp: no free connections available to host")
	// ErrMissingLocation is returned when following a redirect without Location.
	ErrMissingLocation = errors.New("rawhttp: redirect missing Location")
)

// RetryIfErrFunc controls client retries after an error.
// attempts is 1-based. resetTimeout renews DoTimeout budgets when true.
type RetryIfErrFunc func(req *Request, attempts int, err error) (resetTimeout bool, retry bool)

const (
	defaultClientDialTimeout = 3 * time.Second
	// Read/Write timeout 0 means unlimited (HostClient timeout convention).
	defaultMaxConnsPerHost        = 512
	defaultMaxIdleConnDuration    = 10 * time.Second
	defaultMaxResponseBodySize    = 4 << 20
	defaultMaxResponseHeaderBytes = 64 << 10 // 64 KiB
	defaultIdempotentAttempts     = 5
	defaultClientUserAgent        = "rawhttp"
	defaultMaxRedirects           = 0 // do not follow by default
)

// Request is a client HTTP/1.1 request.
type Request struct {
	// Method defaults to GET when empty.
	Method string
	// RequestURI is either an absolute URL (http://host/path) or origin-form (/path).
	// Absolute URLs are required for Client.Do; HostClient accepts origin-form
	// and uses HostClient.Addr as the authority.
	RequestURI string
	// Host sets the Host header. When empty, it is derived from RequestURI / Addr.
	Host string

	headerBuf []byte
	body      []byte

	bodyStream     io.Reader
	bodyStreamSize int // >=0 Content-Length; <0 chunked
	timeout        time.Duration
}

// Reset clears the request for reuse.
func (r *Request) Reset() {
	r.Method = ""
	r.RequestURI = ""
	r.Host = ""
	r.headerBuf = r.headerBuf[:0]
	r.body = nil
	r.bodyStream = nil
	r.bodyStreamSize = 0
	r.timeout = 0
}

// SetTimeout sets a per-request timeout used by Client.Do / HostClient.Do when
// no explicit DoTimeout/DoDeadline is given. Zero means no per-request timeout.
func (r *Request) SetTimeout(d time.Duration) { r.timeout = d }

// GetTimeout returns the per-request timeout (compatibility name).
func (r *Request) GetTimeout() time.Duration { return r.timeout }

// SetBody sets the request body (copied by reference; caller must not mutate
// until Do returns). Clears any SetBodyStream.
func (r *Request) SetBody(b []byte) {
	r.bodyStream = nil
	r.bodyStreamSize = 0
	r.body = b
}

// SetBodyString sets the request body from a string.
func (r *Request) SetBodyString(s string) {
	r.bodyStream = nil
	r.bodyStreamSize = 0
	r.body = append(r.body[:0], s...)
}

// SetBodyStream streams the request body. size >= 0 sends Content-Length;
// size < 0 uses chunked Transfer-Encoding. Clears any SetBody buffer.
func (r *Request) SetBodyStream(reader io.Reader, size int) {
	r.body = nil
	r.bodyStream = reader
	r.bodyStreamSize = size
}

// Body returns the in-memory request body (nil when using SetBodyStream).
func (r *Request) Body() []byte { return r.body }

// SetBasicAuth sets an Authorization: Basic header.
func (r *Request) SetBasicAuth(user, pass string) error {
	token := base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
	return r.SetHeader("Authorization", "Basic "+token)
}

// SetHeader appends a request header. Rejects CR/LF and invalid names.
func (r *Request) SetHeader(key, val string) error {
	if !validHeaderNameStr(key) || containsCTLOrCRLF(val) {
		return ErrHeaderInvalid
	}
	r.headerBuf = append(r.headerBuf, key...)
	r.headerBuf = append(r.headerBuf, ':', ' ')
	r.headerBuf = append(r.headerBuf, val...)
	r.headerBuf = append(r.headerBuf, '\r', '\n')
	return nil
}

// DelHeader removes all headers with the given name (case-insensitive).
func (r *Request) DelHeader(name string) {
	if len(r.headerBuf) == 0 || name == "" {
		return
	}
	dst := r.headerBuf[:0]
	i := 0
	for i < len(r.headerBuf) {
		lineEnd := i
		for lineEnd+1 < len(r.headerBuf) {
			if r.headerBuf[lineEnd] == '\r' && r.headerBuf[lineEnd+1] == '\n' {
				break
			}
			lineEnd++
		}
		if lineEnd+1 >= len(r.headerBuf) {
			break
		}
		line := r.headerBuf[i:lineEnd]
		colon := -1
		for j, c := range line {
			if c == ':' {
				colon = j
				break
			}
		}
		keep := true
		if colon > 0 {
			key := line[:colon]
			if len(key) == len(name) && equalFoldStr(key, name) {
				keep = false
			}
		}
		if keep {
			dst = append(dst, r.headerBuf[i:lineEnd+2]...)
		}
		i = lineEnd + 2
	}
	r.headerBuf = dst
}

func (r *Request) stripCrossOriginCredentials() {
	r.DelHeader("Authorization")
	r.DelHeader("Proxy-Authorization")
	r.DelHeader("Cookie")
}

// Response is a client HTTP/1.1 response.
type Response struct {
	StatusCode int

	headerRaw  []byte // full header block after status line (name: value\r\n)...
	body       []byte
	bodyStream io.ReadCloser // when StreamResponseBody
	connClose  bool
}

// Reset clears the response for reuse.
func (r *Response) Reset() {
	if r.bodyStream != nil {
		_ = r.bodyStream.Close()
		r.bodyStream = nil
	}
	r.StatusCode = 0
	r.headerRaw = r.headerRaw[:0]
	r.body = r.body[:0]
	r.connClose = false
}

// Body returns the response body. When StreamResponseBody was used and the
// stream is still open, Body reads it to completion and closes the stream.
func (r *Response) Body() []byte {
	if r.bodyStream != nil {
		b, err := io.ReadAll(r.bodyStream)
		_ = r.bodyStream.Close()
		r.bodyStream = nil
		if err == nil {
			r.body = append(r.body[:0], b...)
		}
	}
	return r.body
}

// BodyStream returns the streaming response body when StreamResponseBody is
// enabled, otherwise nil. Caller must Close the reader (or call CloseBodyStream)
// so the connection can return to the pool.
func (r *Response) BodyStream() io.ReadCloser { return r.bodyStream }

// CloseBodyStream closes a streaming response body and returns the connection
// to the pool. No-op when not streaming.
func (r *Response) CloseBodyStream() error {
	if r.bodyStream == nil {
		return nil
	}
	err := r.bodyStream.Close()
	r.bodyStream = nil
	return err
}

// IsBodyStream reports whether the response body is streamed.
func (r *Response) IsBodyStream() bool { return r.bodyStream != nil }

// BodyString returns the response body as a string.
func (r *Response) BodyString() string { return string(r.Body()) }

// ConnectionClose reports whether the response requested Connection: close.
func (r *Response) ConnectionClose() bool { return r.connClose }

// Header returns the first matching response header value (case-insensitive), or nil.
func (r *Response) Header(name string) []byte {
	raw := r.headerRaw
	off := 0
	for off < len(raw) {
		eol := off
		for eol < len(raw) && raw[eol] != '\n' {
			eol++
		}
		line := raw[off:eol]
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
		off = eol + 1
		if len(line) == 0 {
			break
		}
		colon := -1
		for i, c := range line {
			if c == ':' {
				colon = i
				break
			}
		}
		if colon <= 0 {
			continue
		}
		if equalFoldStr(line[:colon], name) {
			v := line[colon+1:]
			for len(v) > 0 && (v[0] == ' ' || v[0] == '\t') {
				v = v[1:]
			}
			return v
		}
	}
	return nil
}

// VisitHeader calls f for each response header.
func (r *Response) VisitHeader(f func(key, val []byte)) {
	raw := r.headerRaw
	off := 0
	for off < len(raw) {
		eol := off
		for eol < len(raw) && raw[eol] != '\n' {
			eol++
		}
		line := raw[off:eol]
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
		off = eol + 1
		if len(line) == 0 {
			break
		}
		colon := -1
		for i, c := range line {
			if c == ':' {
				colon = i
				break
			}
		}
		if colon <= 0 {
			continue
		}
		key := line[:colon]
		val := line[colon+1:]
		for len(val) > 0 && (val[0] == ' ' || val[0] == '\t') {
			val = val[1:]
		}
		f(key, val)
	}
}

// Client is an HTTP/1.1 client with per-host keep-alive pools.
type Client struct {
	// Name is the default User-Agent value. Empty → "rawhttp".
	Name string
	// NoDefaultUserAgentHeader suppresses automatic User-Agent.
	NoDefaultUserAgentHeader bool

	// DialTimeout for establishing TCP/TLS connections. Zero → 3s. Negative disables.
	DialTimeout time.Duration
	// ReadTimeout applies after the request is written. Zero disables (unlimited).
	ReadTimeout time.Duration
	// WriteTimeout applies while writing the request. Zero disables (unlimited).
	WriteTimeout time.Duration
	// MaxResponseBodySize caps response bodies. Zero → 4MiB.
	MaxResponseBodySize int
	// MaxResponseHeaderBytes caps response header blocks. Zero → 64KiB.
	MaxResponseHeaderBytes int
	// MaxHeaders caps response header count. Zero → 100.
	MaxHeaders int
	// MaxChunks caps chunked response frames. Zero → 16384.
	MaxChunks int
	// MaxConnsPerHost caps concurrent connections per host. Zero → 512.
	MaxConnsPerHost int
	// MaxIdleConnDuration closes idle pooled connections after this duration. Zero → 10s.
	MaxIdleConnDuration time.Duration
	// MaxConnWaitTimeout limits how long a request waits for a free connection
	// when MaxConnsPerHost is reached. Zero → wait forever. See HostClient.
	MaxConnWaitTimeout time.Duration
	// MaxConnDuration closes pooled connections older than this from dial time.
	// Zero → unlimited. See HostClient.
	MaxConnDuration time.Duration
	// MaxIdemponentCallAttempts retries idempotent methods on connection errors.
	// Zero → 5. Negative → 1. Propagated to HostClient.
	MaxIdemponentCallAttempts int
	// RetryIfErr decides whether to retry after an error. When set, it replaces
	// the default idempotent-method policy. attempts starts at 1.
	RetryIfErr RetryIfErrFunc
	// ConfigureClient is called once when a HostClient is created for a new host.
	ConfigureClient func(hc *HostClient) error
	// WriteBufferSize is propagated to HostClient (0 = direct write).
	WriteBufferSize int
	// ConnPoolStrategy is propagated to HostClient (0 = LIFO).
	ConnPoolStrategy ConnPoolStrategyType
	// DialDualStack is propagated to HostClient.
	DialDualStack bool
	// StreamResponseBody, when true, leaves the response body as a stream
	// (Response.BodyStream). Caller must CloseBodyStream.
	StreamResponseBody bool
	// DisablePathNormalizing, when true, preserves the raw path/query from the
	// absolute URL instead of using net/url cleaned RequestURI.
	DisablePathNormalizing bool
	// Network is the dial network. Empty → "tcp". Propagated to HostClient.
	Network string
	// MaxRedirects is the max number of redirects to follow. Zero means do not follow.
	MaxRedirects int
	// CheckRedirect is called before following a redirect. via holds prior requests
	// (oldest first). Returning a non-nil error stops redirects (use ErrRedirect).
	// nil keeps default auto-follow behavior (with cross-host credential strip).
	CheckRedirect func(req *Request, via []*Request) error
	// TLSConfig is used for https. nil → defaults.
	TLSConfig *tls.Config
	// Dial overrides the default dialer. network is HostClient.Network (or "tcp"); addr is "host:port".
	Dial func(network, addr string) (net.Conn, error)

	mu    sync.Mutex
	hosts map[string]*HostClient

	// Sticky absolute-URI parse cache for the no-redirect hot path.
	stickyURI    string
	stickyScheme string
	stickyHost   string
	stickyPath   string
	stickyHC     *HostClient
}

// Do sends req and fills resp. req.RequestURI must be an absolute URL.
func (c *Client) Do(req *Request, resp *Response) error {
	if req != nil && req.timeout > 0 {
		return c.DoDeadline(req, resp, time.Now().Add(req.timeout))
	}
	if c.MaxRedirects == 0 && c.CheckRedirect == nil {
		return c.doAbsolute(req, resp, time.Time{})
	}
	return c.doWithRedirects(req, resp, c.MaxRedirects, time.Time{})
}

// DoTimeout is Do with a per-attempt deadline applied to dial, write and read.
func (c *Client) DoTimeout(req *Request, resp *Response, timeout time.Duration) error {
	if timeout <= 0 {
		if c.MaxRedirects == 0 && c.CheckRedirect == nil {
			return c.doAbsolute(req, resp, time.Time{})
		}
		return c.doWithRedirects(req, resp, c.MaxRedirects, time.Time{})
	}
	return c.DoDeadline(req, resp, time.Now().Add(timeout))
}

// DoDeadline is Do that must finish before deadline (dial/write/read).
func (c *Client) DoDeadline(req *Request, resp *Response, deadline time.Time) error {
	if c.MaxRedirects == 0 && c.CheckRedirect == nil {
		return c.doAbsolute(req, resp, deadline)
	}
	return c.doWithRedirects(req, resp, c.MaxRedirects, deadline)
}

// doAbsolute is the no-redirect hot path with sticky URL parse + HostClient.Do.
func (c *Client) doAbsolute(req *Request, resp *Response, deadline time.Time) error {
	if req == nil || resp == nil {
		return errors.New("rawhttp: nil request or response")
	}
	uri := req.RequestURI
	host, path := c.stickyHost, c.stickyPath
	hc := c.stickyHC
	if uri != c.stickyURI || hc == nil {
		var (
			scheme string
			err    error
		)
		host, path, scheme, err = splitAbsoluteURL(uri, c.DisablePathNormalizing)
		if err != nil {
			return err
		}
		hc, err = c.hostClientFor(scheme, host)
		if err != nil {
			return err
		}
		c.stickyURI = uri
		c.stickyScheme = scheme
		c.stickyHost = host
		c.stickyPath = path
		c.stickyHC = hc
	}

	origURI, origHost := req.RequestURI, req.Host
	req.RequestURI = path
	if req.Host == "" {
		req.Host = host
	}
	var err error
	if deadline.IsZero() {
		err = hc.Do(req, resp)
	} else {
		err = hc.doDeadline(req, resp, deadline)
	}
	req.RequestURI = origURI
	req.Host = origHost
	return err
}

// Get issues a GET to an absolute URL.
func (c *Client) Get(absURL string, resp *Response) error {
	req := AcquireRequest()
	defer ReleaseRequest(req)
	req.Method = "GET"
	req.RequestURI = absURL
	return c.Do(req, resp)
}

// GetTimeout is Get with a per-attempt deadline.
func (c *Client) GetTimeout(absURL string, resp *Response, timeout time.Duration) error {
	req := AcquireRequest()
	defer ReleaseRequest(req)
	req.Method = "GET"
	req.RequestURI = absURL
	return c.DoTimeout(req, resp, timeout)
}

// Post issues a POST to an absolute URL with optional Content-Type.
func (c *Client) Post(absURL, contentType string, body []byte, resp *Response) error {
	req := AcquireRequest()
	defer ReleaseRequest(req)
	req.Method = "POST"
	req.RequestURI = absURL
	req.SetBody(body)
	if contentType != "" {
		if err := req.SetHeader("Content-Type", contentType); err != nil {
			return err
		}
	}
	return c.Do(req, resp)
}

// PostForm POSTs application/x-www-form-urlencoded form data.
func (c *Client) PostForm(absURL string, form url.Values, resp *Response) error {
	var body []byte
	if form != nil {
		body = []byte(form.Encode())
	}
	return c.Post(absURL, "application/x-www-form-urlencoded", body, resp)
}

// Put issues a PUT to an absolute URL with optional Content-Type.
func (c *Client) Put(absURL, contentType string, body []byte, resp *Response) error {
	return c.doMethodBody("PUT", absURL, contentType, body, resp)
}

// Patch issues a PATCH to an absolute URL with optional Content-Type.
func (c *Client) Patch(absURL, contentType string, body []byte, resp *Response) error {
	return c.doMethodBody("PATCH", absURL, contentType, body, resp)
}

// Delete issues a DELETE to an absolute URL.
func (c *Client) Delete(absURL string, resp *Response) error {
	req := AcquireRequest()
	defer ReleaseRequest(req)
	req.Method = "DELETE"
	req.RequestURI = absURL
	return c.Do(req, resp)
}

func (c *Client) doMethodBody(method, absURL, contentType string, body []byte, resp *Response) error {
	req := AcquireRequest()
	defer ReleaseRequest(req)
	req.Method = method
	req.RequestURI = absURL
	req.SetBody(body)
	if contentType != "" {
		if err := req.SetHeader("Content-Type", contentType); err != nil {
			return err
		}
	}
	return c.Do(req, resp)
}

func (c *Client) doWithRedirects(req *Request, resp *Response, maxRedir int, deadline time.Time) error {
	u, err := url.Parse(req.RequestURI)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return errors.New("rawhttp: RequestURI must be an absolute URL")
	}
	scheme := u.Scheme
	if scheme != "http" && scheme != "https" {
		return errors.New("rawhttp: unsupported URL scheme")
	}

	var via []*Request
	defer func() {
		for _, r := range via {
			ReleaseRequest(r)
		}
	}()
	for {
		if !deadline.IsZero() && time.Now().After(deadline) {
			return ErrTimeout
		}
		prevScheme, prevHost := scheme, u.Host
		hc, err := c.hostClientFor(scheme, u.Host)
		if err != nil {
			return err
		}
		path := u.RequestURI()
		if path == "" {
			path = "/"
		}
		origURI := req.RequestURI
		origHost := req.Host
		req.RequestURI = path
		if req.Host == "" {
			req.Host = u.Host
		}
		if deadline.IsZero() {
			// HostClient.Do includes the simple-GET keep-alive hot path.
			err = hc.Do(req, resp)
		} else {
			err = hc.doDeadline(req, resp, deadline)
		}
		req.RequestURI = origURI
		req.Host = origHost
		if err != nil {
			return err
		}
		if maxRedir <= 0 {
			return nil
		}
		code := resp.StatusCode
		if code != 301 && code != 302 && code != 303 && code != 307 && code != 308 {
			return nil
		}
		loc := resp.Header("Location")
		if len(loc) == 0 {
			return ErrMissingLocation
		}
		next, err := u.Parse(string(loc))
		if err != nil {
			return err
		}
		if next.Scheme == "" || next.Host == "" {
			return ErrMissingLocation
		}
		// Snapshot current request into via before mutating for the next hop.
		snap := AcquireRequest()
		snap.Method = req.Method
		snap.RequestURI = req.RequestURI
		snap.Host = req.Host
		snap.headerBuf = append(snap.headerBuf[:0], req.headerBuf...)
		via = append(via, snap)

		if next.Scheme != prevScheme || !strings.EqualFold(next.Host, prevHost) {
			req.stripCrossOriginCredentials()
		}
		u = next
		scheme = next.Scheme
		req.RequestURI = next.String()
		req.Host = ""
		if code == 303 {
			req.Method = "GET"
			req.body = nil
			req.bodyStream = nil
			req.bodyStreamSize = 0
		}
		if c.CheckRedirect != nil {
			if err := c.CheckRedirect(req, via); err != nil {
				return err
			}
		}
		maxRedir--
		resp.Reset()
	}
}

func (c *Client) hostClientFor(scheme, host string) (*HostClient, error) {
	key := scheme + "|" + host
	c.mu.Lock()
	if c.hosts == nil {
		c.hosts = make(map[string]*HostClient)
	}
	hc := c.hosts[key]
	if hc == nil {
		hc = &HostClient{
			Addr:                      host,
			IsTLS:                     scheme == "https",
			Name:                      c.Name,
			NoDefaultUserAgentHeader:  c.NoDefaultUserAgentHeader,
			DialTimeout:               c.DialTimeout,
			ReadTimeout:               c.ReadTimeout,
			WriteTimeout:              c.WriteTimeout,
			MaxResponseBodySize:       c.MaxResponseBodySize,
			MaxResponseHeaderBytes:    c.MaxResponseHeaderBytes,
			MaxHeaders:                c.MaxHeaders,
			MaxChunks:                 c.MaxChunks,
			MaxConns:                  c.MaxConnsPerHost,
			MaxIdleConnDuration:       c.MaxIdleConnDuration,
			MaxConnWaitTimeout:        c.MaxConnWaitTimeout,
			MaxConnDuration:           c.MaxConnDuration,
			MaxIdemponentCallAttempts: c.MaxIdemponentCallAttempts,
			RetryIfErr:                c.RetryIfErr,
			WriteBufferSize:           c.WriteBufferSize,
			ConnPoolStrategy:          c.ConnPoolStrategy,
			DialDualStack:             c.DialDualStack,
			StreamResponseBody:        c.StreamResponseBody,
			DisablePathNormalizing:    c.DisablePathNormalizing,
			Network:                   c.Network,
			TLSConfig:                 c.TLSConfig,
			Dial:                      c.Dial,
		}
		if c.ConfigureClient != nil {
			if err := c.ConfigureClient(hc); err != nil {
				c.mu.Unlock()
				return nil, err
			}
		}
		c.hosts[key] = hc
	}
	c.mu.Unlock()
	return hc, nil
}

var (
	requestPool  = sync.Pool{New: func() any { return new(Request) }}
	responsePool = sync.Pool{New: func() any { return new(Response) }}
)

// AcquireRequest gets a Request from the pool.
func AcquireRequest() *Request {
	r := requestPool.Get().(*Request)
	r.Reset()
	return r
}

// ReleaseRequest returns a Request to the pool.
func ReleaseRequest(r *Request) {
	if r == nil {
		return
	}
	r.Reset()
	requestPool.Put(r)
}

// AcquireResponse gets a Response from the pool.
func AcquireResponse() *Response {
	r := responsePool.Get().(*Response)
	r.Reset()
	return r
}

// ReleaseResponse returns a Response to the pool.
func ReleaseResponse(r *Response) {
	if r == nil {
		return
	}
	r.Reset()
	responsePool.Put(r)
}

// splitAbsoluteURL returns host, origin-form path, and scheme.
// When disableNorm is true, the path/query after the authority is kept raw
// (no net/url cleaning of "." / ".." segments).
func splitAbsoluteURL(abs string, disableNorm bool) (host, path, scheme string, err error) {
	if !disableNorm {
		u, perr := url.Parse(abs)
		if perr != nil || u.Scheme == "" || u.Host == "" {
			return "", "", "", errors.New("rawhttp: RequestURI must be an absolute URL")
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return "", "", "", errors.New("rawhttp: unsupported URL scheme")
		}
		path = u.RequestURI()
		if path == "" {
			path = "/"
		}
		return u.Host, path, u.Scheme, nil
	}
	schemeEnd := strings.Index(abs, "://")
	if schemeEnd <= 0 {
		return "", "", "", errors.New("rawhttp: RequestURI must be an absolute URL")
	}
	scheme = abs[:schemeEnd]
	if scheme != "http" && scheme != "https" {
		return "", "", "", errors.New("rawhttp: unsupported URL scheme")
	}
	rest := abs[schemeEnd+3:]
	if i := strings.IndexByte(rest, '#'); i >= 0 {
		rest = rest[:i]
	}
	slash := strings.IndexByte(rest, '/')
	qmark := strings.IndexByte(rest, '?')
	end := len(rest)
	if slash >= 0 && slash < end {
		end = slash
	}
	if qmark >= 0 && qmark < end {
		end = qmark
	}
	auth := rest[:end]
	path = rest[end:]
	if at := strings.LastIndexByte(auth, '@'); at >= 0 {
		auth = auth[at+1:]
	}
	if auth == "" {
		return "", "", "", errors.New("rawhttp: RequestURI must be an absolute URL")
	}
	if path == "" {
		path = "/"
	}
	return auth, path, scheme, nil
}
