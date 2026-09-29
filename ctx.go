package rawhttp

import (
	"bytes"
	"crypto/tls"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Ctx holds one request/response cycle. Values pointing into the connection
// buffer (Method, Path, Query, Header results) are only valid during Handler.
type Ctx struct {
	Method []byte
	Path   []byte // path without query string
	Query  []byte // raw query without leading '?', may be nil

	httpMinor       int
	contentLength   int // set from Content-Length; 0 if absent/chunked
	keepAliveHeader bool
	closeHeader     bool
	chunked         bool
	sawHost         bool
	expectContinue  bool
	clSet           bool // Content-Length seen at least once
	head            bool // HEAD method
	forceClose      bool // SetConnectionClose
	respChunked     bool // Transfer-Encoding: chunked response

	headerBlock []byte
	hdrCopy     []byte
	methodCopy  []byte
	pathCopy    []byte
	queryCopy   []byte
	host        []byte
	hostCopy    []byte
	uriBuf      []byte

	// Indexed request headers (zero-copy into headerBlock / host).
	reqContentType []byte
	userAgent      []byte
	accept         []byte
	acceptEncoding []byte
	cookieHdr      []byte
	referer        []byte
	authorization  []byte
	origin         []byte
	ctCopy         []byte
	uaCopy         []byte
	acceptCopy     []byte
	acceptEncCopy  []byte
	cookieCopy     []byte
	refererCopy    []byte
	authCopy       []byte
	originCopy     []byte

	reqBody   []byte
	reqStream *requestStream // when Server.StreamRequestBody

	StatusCode int

	respBuf   []byte
	respBody  []byte
	bodyIsRef bool

	respHeaderBuf []byte
	contentType   []byte
	userSetCT     bool

	outBuf        []byte
	cachedCL      int
	cachedCode    int
	cachedDateSec int64
	cachedDateOff int
	cachedServer  string
	cachedBodyPtr *byte
	cachedHasCT   bool
	cachedCT      []byte
	cacheOK       bool
	noDefaultDate bool
	noDefaultCT   bool
	serverName    string
	timedOut      bool
	timeoutGuard  bool // TimeoutHandler enables respMu on write paths
	hijacked      bool
	respMu        sync.Mutex

	// Non-indexed request headers (zero-copy into headerBlock) for O(n) peek
	// without re-tokenizing the header block on every Header() call.
	extraKeys [][]byte
	extraVals [][]byte

	streamBody   io.Reader
	streamSize   int // >=0 Content-Length; -1 chunked stream
	writeBufSize int // Server.WriteBufferSize for stream responses

	conn net.Conn
	cr   *connReader

	remoteAddr string
	localAddr  string
	isTLS      bool

	connID         uint64
	connRequestNum uint64
	connTime       time.Time
	handlerTime    time.Time

	multipart       *MultipartForm
	multipartNative *multipart.Form // underlying mime form for RemoveAll

	maxMultipartMemory int64
	maxMultipartFiles  int
	maxMultipartParts  int

	trustedProxies []*net.IPNet
	userValues     map[any]any
	uri            *URI
	hijackNoResp   bool
	formValueFunc  FormValueFunc
}

func (c *Ctx) reset() {
	c.Method = nil
	c.Path = nil
	c.Query = nil
	c.httpMinor = 1
	c.contentLength = 0
	c.keepAliveHeader = false
	c.closeHeader = false
	c.chunked = false
	c.sawHost = false
	c.expectContinue = false
	c.clSet = false
	c.head = false
	c.forceClose = false
	c.respChunked = false
	c.headerBlock = nil
	c.host = nil
	c.reqContentType = nil
	c.userAgent = nil
	c.accept = nil
	c.acceptEncoding = nil
	c.cookieHdr = nil
	c.referer = nil
	c.authorization = nil
	c.origin = nil
	c.reqBody = c.reqBody[:0]
	c.reqStream = nil
	c.StatusCode = 200
	c.respBuf = c.respBuf[:0]
	c.respBody = nil
	c.bodyIsRef = false
	c.respHeaderBuf = c.respHeaderBuf[:0]
	c.contentType = c.contentType[:0]
	c.userSetCT = false
	c.cacheOK = true
	c.cachedHasCT = false
	c.cachedCT = c.cachedCT[:0]
	c.noDefaultDate = false
	c.noDefaultCT = false
	c.serverName = ""
	c.extraKeys = c.extraKeys[:0]
	c.extraVals = c.extraVals[:0]
	c.timedOut = false
	c.timeoutGuard = false
	c.hijacked = false
	c.streamBody = nil
	c.streamSize = 0
	c.writeBufSize = 0
	c.connID = 0
	c.connRequestNum = 0
	c.connTime = time.Time{}
	c.handlerTime = time.Time{}
	c.conn = nil
	c.cr = nil
	c.maxMultipartMemory = 0
	c.maxMultipartFiles = 0
	c.maxMultipartParts = 0
	c.trustedProxies = nil
	c.formValueFunc = nil
	if c.userValues != nil {
		clear(c.userValues)
	}
	if c.uri != nil {
		c.uri.Reset()
	}
	c.hijackNoResp = false
	releaseMultipart(c)
}

func (c *Ctx) lockResp() {
	if c.timeoutGuard {
		c.respMu.Lock()
	}
}

func (c *Ctx) unlockResp() {
	if c.timeoutGuard {
		c.respMu.Unlock()
	}
}

// Header returns a request header value (case-insensitive), or nil.
// Common headers are O(1) via the parse-time index.
func (c *Ctx) Header(name string) []byte {
	switch len(name) {
	case 4:
		if equalFoldStrASCII(name, "Host") {
			return c.host
		}
	case 6:
		if equalFoldStrASCII(name, "Accept") {
			return c.accept
		}
		if equalFoldStrASCII(name, "Cookie") {
			return c.cookieHdr
		}
		if equalFoldStrASCII(name, "Origin") {
			return c.origin
		}
	case 7:
		if equalFoldStrASCII(name, "Referer") {
			return c.referer
		}
	case 10:
		if equalFoldStrASCII(name, "User-Agent") {
			return c.userAgent
		}
	case 12:
		if equalFoldStrASCII(name, "Content-Type") {
			return c.reqContentType
		}
	case 13:
		if equalFoldStrASCII(name, "Authorization") {
			return c.authorization
		}
	case 15:
		if equalFoldStrASCII(name, "Accept-Encoding") {
			return c.acceptEncoding
		}
	}
	for i, k := range c.extraKeys {
		if equalFoldStr(k, name) {
			return c.extraVals[i]
		}
	}
	return c.headerScan(name)
}

// Peek is an alias for Header (fasthttp-style).
func (c *Ctx) Peek(name string) []byte { return c.Header(name) }

func equalFoldStrASCII(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		x, y := a[i], b[i]
		if x == y {
			continue
		}
		if x >= 'A' && x <= 'Z' {
			x += 'a' - 'A'
		}
		if y >= 'A' && y <= 'Z' {
			y += 'a' - 'A'
		}
		if x != y {
			return false
		}
	}
	return true
}

func (c *Ctx) headerScan(name string) []byte {
	block := c.headerBlock
	nlen := len(name)
	off := 0
	for off < len(block) {
		nl := bytes.IndexByte(block[off:], '\n')
		if nl < 0 {
			break
		}
		line := block[off : off+nl]
		off += nl + 1
		if n := len(line); n > 0 && line[n-1] == '\r' {
			line = line[:n-1]
		}
		if len(line) == 0 {
			break
		}
		if len(line) < nlen+1 || line[nlen] != ':' {
			continue
		}
		if !equalFoldStr(line[:nlen], name) {
			continue
		}
		val := line[nlen+1:]
		if len(val) > 0 && (val[0] == ' ' || val[0] == '\t') {
			val = val[1:]
		}
		return val
	}
	return nil
}

// UserAgent returns the User-Agent request header (zero-copy), or nil.
func (c *Ctx) UserAgent() []byte { return c.userAgent }

// RequestContentType returns the Content-Type request header (zero-copy), or nil.
func (c *Ctx) RequestContentType() []byte { return c.reqContentType }

// Referer returns the Referer request header (zero-copy), or nil.
func (c *Ctx) Referer() []byte { return c.referer }

// AcceptEncoding returns the Accept-Encoding request header (zero-copy), or nil.
func (c *Ctx) AcceptEncoding() []byte { return c.acceptEncoding }

// Accept returns the Accept request header (zero-copy), or nil.
func (c *Ctx) Accept() []byte { return c.accept }

// Authorization returns the Authorization request header (zero-copy), or nil.
func (c *Ctx) Authorization() []byte { return c.authorization }

// Origin returns the Origin request header (zero-copy), or nil.
func (c *Ctx) Origin() []byte { return c.origin }

// SetUserValue stores a request-scoped value (allocates a map on first use).
func (c *Ctx) SetUserValue(key, val any) {
	if c.userValues == nil {
		c.userValues = make(map[any]any)
	}
	c.userValues[key] = val
}

// UserValue returns a value previously stored with SetUserValue.
func (c *Ctx) UserValue(key any) any {
	if c.userValues == nil {
		return nil
	}
	return c.userValues[key]
}

// VisitUserValues calls f for each request-scoped value.
func (c *Ctx) VisitUserValues(f func(key, val any)) {
	for k, v := range c.userValues {
		f(k, v)
	}
}

// AppendBody appends p to the response body.
func (c *Ctx) AppendBody(p []byte) {
	c.Write(p)
}

// AppendBodyString appends s to the response body.
func (c *Ctx) AppendBodyString(s string) {
	c.WriteString(s)
}

// AppendUint appends the decimal form of n to the response body without fmt.
func (c *Ctx) AppendUint(n uint64) {
	c.lockResp()
	defer c.unlockResp()
	if c.timedOut {
		return
	}
	c.streamBody = nil
	c.bodyIsRef = false
	c.cacheOK = false
	if c.respChunked {
		var tmp [32]byte
		b := strconv.AppendUint(tmp[:0], n, 10)
		c.respBuf = appendChunk(c.respBuf, b)
		c.respBody = c.respBuf
		return
	}
	c.respBuf = strconv.AppendUint(c.respBuf, n, 10)
	c.respBody = c.respBuf
}

func (c *Ctx) Body() []byte { return c.reqBody }

// RequestBodyStream returns an io.Reader for the request body.
// When Server.StreamRequestBody is true, this is a live connection reader
// (must be consumed or drained). Otherwise it wraps the buffered Body().
// Returns nil when there is no body.
func (c *Ctx) RequestBodyStream() io.Reader {
	if c.reqStream != nil {
		return c.reqStream
	}
	if len(c.reqBody) == 0 {
		return nil
	}
	return &bytesReader{b: c.reqBody}
}

// bytesReader is a tiny zero-alloc alternative to bytes.NewReader for Body wrap.
type bytesReader struct {
	b []byte
	i int
}

func (r *bytesReader) Read(p []byte) (int, error) {
	if r.i >= len(r.b) {
		return 0, io.EOF
	}
	n := copy(p, r.b[r.i:])
	r.i += n
	return n, nil
}

// RemoteAddr returns the connection's remote address string (e.g. "1.2.3.4:5678").
func (c *Ctx) RemoteAddr() string { return c.remoteAddr }

// RemoteIP returns the remote host without port (IPv6 brackets stripped).
func (c *Ctx) RemoteIP() string {
	s := c.remoteAddr
	if len(s) == 0 {
		return ""
	}
	if s[0] == '[' {
		for i := 1; i < len(s); i++ {
			if s[i] == ']' {
				return s[1:i]
			}
		}
		return s
	}
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == ':' {
			return s[:i]
		}
	}
	return s
}

// LocalAddr returns the connection's local address string.
func (c *Ctx) LocalAddr() string { return c.localAddr }

// IsTLS reports whether the connection is TLS.
func (c *Ctx) IsTLS() bool { return c.isTLS }

// TLSConnectionState returns the TLS state, or nil when not TLS.
func (c *Ctx) TLSConnectionState() *tls.ConnectionState {
	tc, ok := c.conn.(*tls.Conn)
	if !ok || tc == nil {
		return nil
	}
	st := tc.ConnectionState()
	return &st
}

// Conn returns the underlying net.Conn. Prefer Hijack for ownership transfer.
// Reading/writing the conn while the server still owns it is unsafe.
func (c *Ctx) Conn() net.Conn { return c.conn }

// ConnID returns a unique id for the connection (stable across keep-alive requests).
func (c *Ctx) ConnID() uint64 { return c.connID }

// ConnRequestNum returns the 1-based request sequence number on this connection.
func (c *Ctx) ConnRequestNum() uint64 { return c.connRequestNum }

// ConnTime returns when the server started serving this connection.
func (c *Ctx) ConnTime() time.Time { return c.connTime }

// Time returns when the handler was invoked. Zero unless the server recorded it
// (avoided on the default hot path); falls back to ConnTime when unset.
func (c *Ctx) Time() time.Time {
	if !c.handlerTime.IsZero() {
		return c.handlerTime
	}
	return c.connTime
}

// ID returns a unique request id derived from ConnID and ConnRequestNum.
func (c *Ctx) ID() uint64 { return (c.connID << 32) | c.connRequestNum }

// IsHTTP11 reports whether the request used HTTP/1.1 (vs 1.0).
func (c *Ctx) IsHTTP11() bool { return c.httpMinor >= 1 }

// Host returns the request Host header value (zero-copy).
func (c *Ctx) Host() []byte { return c.host }

// RequestURI returns path + optional "?" + query. When a query is present the
// result is copied into an internal buffer; otherwise Path is returned as-is.
func (c *Ctx) RequestURI() []byte {
	if len(c.Query) == 0 {
		return c.Path
	}
	c.uriBuf = c.uriBuf[:0]
	c.uriBuf = append(c.uriBuf, c.Path...)
	c.uriBuf = append(c.uriBuf, '?')
	c.uriBuf = append(c.uriBuf, c.Query...)
	return c.uriBuf
}

// VisitHeader calls f for each request header. key/val are valid only until f returns.
func (c *Ctx) VisitHeader(f func(key, val []byte)) {
	block := c.headerBlock
	off := 0
	for off < len(block) {
		nl := bytes.IndexByte(block[off:], '\n')
		if nl < 0 {
			break
		}
		line := block[off : off+nl]
		off += nl + 1
		if n := len(line); n > 0 && line[n-1] == '\r' {
			line = line[:n-1]
		}
		if len(line) == 0 {
			break
		}
		colon := bytes.IndexByte(line, ':')
		if colon <= 0 {
			continue
		}
		key := line[:colon]
		val := line[colon+1:]
		if len(val) > 0 && (val[0] == ' ' || val[0] == '\t') {
			val = val[1:]
		}
		f(key, val)
	}
}

// SetConnectionClose forces Connection: close on the response.
func (c *Ctx) SetConnectionClose() {
	c.lockResp()
	defer c.unlockResp()
	if c.timedOut {
		return
	}
	c.forceClose = true
	c.cacheOK = false
}

// Redirect responds with a redirect. code defaults to 302 when zero.
func (c *Ctx) Redirect(url string, code int) error {
	if code == 0 {
		code = 302
	}
	if !validRedirectURL(url) {
		return ErrHeaderInvalid
	}
	c.SetStatusCode(code)
	c.lockResp()
	if !c.timedOut {
		c.respBody = nil
		c.streamBody = nil
		c.bodyIsRef = false
		c.respBuf = c.respBuf[:0]
	}
	c.unlockResp()
	return c.SetHeader("Location", url)
}

// validRedirectURL rejects CRLF / CTL and scheme-relative open redirects ("//evil").
// Absolute http(s) and relative paths are allowed.
func validRedirectURL(url string) bool {
	if url == "" || containsCTLOrCRLF(url) {
		return false
	}
	if len(url) >= 2 && url[0] == '/' && url[1] == '/' {
		return false
	}
	return true
}

// Error sets status code and a plain-text body.
func (c *Ctx) Error(msg string, code int) {
	c.lockResp()
	defer c.unlockResp()
	if c.timedOut {
		return
	}
	if code < 100 || code > 999 {
		code = 500
	}
	c.StatusCode = code
	c.contentType = append(c.contentType[:0], "text/plain; charset=utf-8"...)
	c.userSetCT = true
	c.respChunked = false
	c.streamBody = nil
	c.bodyIsRef = false
	c.cacheOK = false
	c.respBuf = append(c.respBuf[:0], msg...)
	c.respBody = c.respBuf
}

// NotFound is shorthand for Error("Not Found", 404).
func (c *Ctx) NotFound() { c.Error("Not Found", 404) }

// NotModified resets the response body and sets 304.
func (c *Ctx) NotModified() {
	c.lockResp()
	defer c.unlockResp()
	if c.timedOut {
		return
	}
	c.StatusCode = 304
	c.respChunked = false
	c.streamBody = nil
	c.bodyIsRef = false
	c.respBuf = c.respBuf[:0]
	c.respBody = nil
	c.cacheOK = false
}

// Success sets Content-Type and body (status remains current, typically 200).
func (c *Ctx) Success(contentType string, body []byte) {
	c.SetContentType(contentType)
	c.SetBody(body)
}

// SuccessString sets Content-Type and body from strings.
func (c *Ctx) SuccessString(contentType, body string) {
	c.SetContentType(contentType)
	c.SetBodyString(body)
}

// IfModifiedSince reports whether the resource should be sent: true when the
// If-Modified-Since header is missing/invalid or lastModified is newer.
func (c *Ctx) IfModifiedSince(lastModified time.Time) bool {
	ims := c.Header("If-Modified-Since")
	if len(ims) == 0 {
		return true
	}
	t, err := http.ParseTime(string(ims))
	if err != nil {
		return true
	}
	return lastModified.UTC().Truncate(time.Second).After(t)
}

// Forbidden is shorthand for Error("Forbidden", 403).
func (c *Ctx) Forbidden() { c.Error("Forbidden", 403) }

// Unauthorized is shorthand for Error("Unauthorized", 401).
func (c *Ctx) Unauthorized() { c.Error("Unauthorized", 401) }

// BadRequest is shorthand for Error("Bad Request", 400).
func (c *Ctx) BadRequest() { c.Error("Bad Request", 400) }

// TooManyRequests is shorthand for Error("Too Many Requests", 429).
func (c *Ctx) TooManyRequests() { c.Error("Too Many Requests", 429) }

// MethodNotAllowed sets 405 and an Allow header (e.g. "GET, HEAD").
func (c *Ctx) MethodNotAllowed(allow string) error {
	c.Error("Method Not Allowed", 405)
	return c.SetHeader("Allow", allow)
}

// SetChunked enables Transfer-Encoding: chunked for the response.
// Subsequent Write/WriteString calls append framed chunks.
func (c *Ctx) SetChunked() {
	c.lockResp()
	defer c.unlockResp()
	if c.timedOut {
		return
	}
	c.respChunked = true
	c.streamBody = nil
	c.bodyIsRef = false
	c.cacheOK = false
	c.respBody = c.respBuf[:0]
	c.respBuf = c.respBuf[:0]
}

func (c *Ctx) ContentLength() int {
	if c.chunked {
		return -1
	}
	return c.contentLength
}

func (c *Ctx) QueryArgs() QueryArgs { return QueryArgs{raw: c.Query} }

func (c *Ctx) SetStatusCode(code int) {
	c.lockResp()
	defer c.unlockResp()
	if c.timedOut {
		return
	}
	if code < 100 || code > 999 {
		code = 500
	}
	c.StatusCode = code
	c.cacheOK = false
}

func (c *Ctx) SetContentType(ct string) {
	if !c.timeoutGuard {
		if c.timedOut {
			return
		}
		if containsCTLOrCRLF(ct) {
			return
		}
		c.contentType = append(c.contentType[:0], ct...)
		c.userSetCT = true
		return
	}
	c.lockResp()
	defer c.unlockResp()
	if c.timedOut {
		return
	}
	if containsCTLOrCRLF(ct) {
		return // ignore unsafe values
	}
	c.contentType = append(c.contentType[:0], ct...)
	c.userSetCT = true
}

// SetETag sets the ETag response header.
func (c *Ctx) SetETag(etag string) error {
	return c.SetHeader("ETag", etag)
}

// SetLastModified sets Last-Modified from t (HTTP-date GMT).
func (c *Ctx) SetLastModified(t time.Time) error {
	var b [32]byte
	buf := appendHTTPDate(b[:0], t.UTC())
	return c.SetHeader("Last-Modified", string(buf))
}

// SetHeader appends a response header. Returns ErrHeaderInvalid if key is not
// a valid token or key/val contains CTL/CR/LF (response-splitting protection).
func (c *Ctx) SetHeader(key, val string) error {
	c.lockResp()
	defer c.unlockResp()
	if c.timedOut {
		return nil
	}
	if !validHeaderNameStr(key) || containsCTLOrCRLF(val) {
		return ErrHeaderInvalid
	}
	c.respHeaderBuf = append(c.respHeaderBuf, key...)
	c.respHeaderBuf = append(c.respHeaderBuf, ':', ' ')
	c.respHeaderBuf = append(c.respHeaderBuf, val...)
	c.respHeaderBuf = append(c.respHeaderBuf, '\r', '\n')
	c.cacheOK = false
	return nil
}

// AddHeader is an alias for SetHeader (appends another header line).
func (c *Ctx) AddHeader(key, val string) error { return c.SetHeader(key, val) }

// DelHeader removes all response headers with the given name (case-insensitive)
// from the pending response header buffer.
func (c *Ctx) DelHeader(name string) {
	c.lockResp()
	defer c.unlockResp()
	if c.timedOut || len(name) == 0 || len(c.respHeaderBuf) == 0 {
		return
	}
	buf := c.respHeaderBuf
	out := buf[:0]
	off := 0
	for off < len(buf) {
		nl := bytes.IndexByte(buf[off:], '\n')
		if nl < 0 {
			out = append(out, buf[off:]...)
			break
		}
		line := buf[off : off+nl+1]
		off += nl + 1
		trim := line
		if n := len(trim); n >= 2 && trim[n-2] == '\r' {
			trim = trim[:n-2]
		} else if n := len(trim); n >= 1 && trim[n-1] == '\n' {
			trim = trim[:n-1]
		}
		colon := bytes.IndexByte(trim, ':')
		if colon > 0 && equalFoldStr(trim[:colon], name) {
			c.cacheOK = false
			continue
		}
		out = append(out, line...)
	}
	c.respHeaderBuf = out
}

// SetBodyStream sets the response body from r. When size >= 0 a Content-Length
// is sent and exactly size bytes are read. When size < 0 the body is sent with
// Transfer-Encoding: chunked until EOF.
func (c *Ctx) SetBodyStream(r io.Reader, size int) {
	c.lockResp()
	defer c.unlockResp()
	if c.timedOut {
		return
	}
	c.respChunked = size < 0
	c.streamBody = r
	c.streamSize = size
	c.respBody = nil
	c.bodyIsRef = false
	c.cacheOK = false
}

func validHeaderNameStr(s string) bool {
	if len(s) == 0 || containsCRLF(s) {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' {
			continue
		}
		switch c {
		case '!', '#', '$', '%', '&', '\'', '*', '+', '.', '^', '_', '`', '|', '~':
			continue
		default:
			return false
		}
	}
	return true
}

func containsCRLF(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '\r' || s[i] == '\n' {
			return true
		}
	}
	return false
}

func containsCTLOrCRLF(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x20 || c == 0x7f {
			return true
		}
	}
	return false
}

func (c *Ctx) Write(p []byte) {
	c.lockResp()
	defer c.unlockResp()
	if c.timedOut {
		return
	}
	c.streamBody = nil
	c.bodyIsRef = false
	c.cacheOK = false
	if c.respChunked {
		if len(p) == 0 {
			return
		}
		c.respBuf = appendChunk(c.respBuf, p)
		c.respBody = c.respBuf
		return
	}
	c.respBuf = append(c.respBuf, p...)
	c.respBody = c.respBuf
}

func (c *Ctx) WriteString(s string) {
	c.lockResp()
	defer c.unlockResp()
	if c.timedOut {
		return
	}
	c.streamBody = nil
	c.bodyIsRef = false
	c.cacheOK = false
	if c.respChunked {
		if len(s) == 0 {
			return
		}
		c.respBuf = appendChunkString(c.respBuf, s)
		c.respBody = c.respBuf
		return
	}
	c.respBuf = append(c.respBuf, s...)
	c.respBody = c.respBuf
}

func (c *Ctx) SetBody(b []byte) {
	if !c.timeoutGuard {
		if c.timedOut {
			return
		}
		c.respChunked = false
		c.streamBody = nil
		c.respBody = b
		c.bodyIsRef = true
		return
	}
	c.lockResp()
	defer c.unlockResp()
	if c.timedOut {
		return
	}
	c.respChunked = false
	c.streamBody = nil
	c.respBody = b
	c.bodyIsRef = true
}

// ResponseBody returns the pending in-memory response body (not streams).
func (c *Ctx) ResponseBody() []byte {
	c.lockResp()
	defer c.unlockResp()
	return c.respBody
}

// SetBodyString sets the response body from a string (copied into an internal buffer).
func (c *Ctx) SetBodyString(s string) {
	c.lockResp()
	defer c.unlockResp()
	if c.timedOut {
		return
	}
	c.respChunked = false
	c.streamBody = nil
	c.bodyIsRef = false
	c.cacheOK = false
	c.respBuf = append(c.respBuf[:0], s...)
	c.respBody = c.respBuf
}

// PathEqual reports whether Path equals p (exact, case-sensitive).
func (c *Ctx) PathEqual(p string) bool {
	if len(c.Path) != len(p) {
		return false
	}
	for i := 0; i < len(p); i++ {
		if c.Path[i] != p[i] {
			return false
		}
	}
	return true
}

// PathHasPrefix reports whether Path begins with prefix.
func (c *Ctx) PathHasPrefix(prefix string) bool {
	if len(c.Path) < len(prefix) {
		return false
	}
	for i := 0; i < len(prefix); i++ {
		if c.Path[i] != prefix[i] {
			return false
		}
	}
	return true
}

// MethodEqual reports whether the request method equals m (ASCII, case-insensitive).
func (c *Ctx) MethodEqual(m string) bool {
	return equalFoldStr(c.Method, m)
}

// IsGet reports whether the method is GET (not HEAD).
func (c *Ctx) IsGet() bool { return !c.head && equalFoldStr(c.Method, "GET") }

// IsHead reports whether this is a HEAD request.
func (c *Ctx) IsHead() bool { return c.head }

// IsPost reports whether the method is POST.
func (c *Ctx) IsPost() bool { return equalFoldStr(c.Method, "POST") }

// IsPut reports whether the method is PUT.
func (c *Ctx) IsPut() bool { return equalFoldStr(c.Method, "PUT") }

// IsDelete reports whether the method is DELETE.
func (c *Ctx) IsDelete() bool { return equalFoldStr(c.Method, "DELETE") }

// IsPatch reports whether the method is PATCH.
func (c *Ctx) IsPatch() bool { return equalFoldStr(c.Method, "PATCH") }

// IsOptions reports whether the method is OPTIONS.
func (c *Ctx) IsOptions() bool { return equalFoldStr(c.Method, "OPTIONS") }

func (c *Ctx) shouldClose(disableKeepalive bool) bool {
	if disableKeepalive || c.closeHeader || c.forceClose {
		return true
	}
	if c.httpMinor == 0 && !c.keepAliveHeader {
		return true
	}
	return false
}

func equalFoldStr(a []byte, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		x, y := a[i], b[i]
		if x == y {
			continue
		}
		if x >= 'A' && x <= 'Z' {
			x += 'a' - 'A'
		}
		if y >= 'A' && y <= 'Z' {
			y += 'a' - 'A'
		}
		if x != y {
			return false
		}
	}
	return true
}

type QueryArgs struct {
	raw []byte
}

func (q QueryArgs) Get(key string) []byte {
	raw := q.raw
	off := 0
	for off <= len(raw) {
		end := off
		for end < len(raw) && raw[end] != '&' {
			end++
		}
		pair := raw[off:end]
		eq := bytes.IndexByte(pair, '=')
		var k, v []byte
		if eq < 0 {
			k = pair
		} else {
			k = pair[:eq]
			v = pair[eq+1:]
		}
		if len(k) == len(key) {
			match := true
			for i := 0; i < len(k); i++ {
				if k[i] != key[i] {
					match = false
					break
				}
			}
			if match {
				return v
			}
		}
		if end >= len(raw) {
			break
		}
		off = end + 1
	}
	return nil
}

func (q QueryArgs) Peek(key string) []byte { return q.Get(key) }

// GetString returns the query value as a string, or "".
func (q QueryArgs) GetString(key string) string {
	v := q.Get(key)
	if v == nil {
		return ""
	}
	return string(v)
}

// GetUint parses the query value as an unsigned decimal integer.
// Missing/empty/invalid → (0, false).
func (q QueryArgs) GetUint(key string) (uint64, bool) {
	v := q.Get(key)
	if len(v) == 0 {
		return 0, false
	}
	var n uint64
	for _, c := range v {
		if c < '0' || c > '9' {
			return 0, false
		}
		d := uint64(c - '0')
		if n > (^uint64(0)-d)/10 {
			return 0, false
		}
		n = n*10 + d
	}
	return n, true
}

// GetBool treats 1/true/yes/on (case-insensitive) as true; missing → false.
func (q QueryArgs) GetBool(key string) bool {
	v := q.Get(key)
	if len(v) == 0 {
		return false
	}
	if len(v) == 1 && v[0] == '1' {
		return true
	}
	return equalFoldStr(v, "true") || equalFoldStr(v, "yes") || equalFoldStr(v, "on")
}

// Len returns the number of query pairs (including empty keys/values).
func (q QueryArgs) Len() int {
	raw := q.raw
	if len(raw) == 0 {
		return 0
	}
	n := 1
	for i := 0; i < len(raw); i++ {
		if raw[i] == '&' {
			n++
		}
	}
	return n
}

// Has reports whether key is present (even with an empty value).
func (q QueryArgs) Has(key string) bool {
	raw := q.raw
	off := 0
	for off <= len(raw) {
		end := off
		for end < len(raw) && raw[end] != '&' {
			end++
		}
		pair := raw[off:end]
		eq := bytes.IndexByte(pair, '=')
		var k []byte
		if eq < 0 {
			k = pair
		} else {
			k = pair[:eq]
		}
		if len(k) == len(key) {
			match := true
			for i := 0; i < len(k); i++ {
				if k[i] != key[i] {
					match = false
					break
				}
			}
			if match {
				return true
			}
		}
		if end >= len(raw) {
			break
		}
		off = end + 1
	}
	return false
}

// Visit calls f for each query pair. key/val are valid only until f returns.
func (q QueryArgs) Visit(f func(key, val []byte)) {
	raw := q.raw
	off := 0
	for off <= len(raw) {
		end := off
		for end < len(raw) && raw[end] != '&' {
			end++
		}
		pair := raw[off:end]
		eq := bytes.IndexByte(pair, '=')
		var k, v []byte
		if eq < 0 {
			k = pair
		} else {
			k = pair[:eq]
			v = pair[eq+1:]
		}
		if len(k) > 0 || len(v) > 0 || len(pair) > 0 {
			f(k, v)
		}
		if end >= len(raw) {
			break
		}
		off = end + 1
	}
}
