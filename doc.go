// Package rawhttp is a high-performance HTTP/1.1 server and client.
//
// Design:
//  1. No bufio — hand-rolled connReader with zero-copy Method/Path/headers.
//  2. Pin/off separation so compaction cannot corrupt live header slices.
//  3. GET/no-body requests keep zero-copy headers through Handler. When a
//     body is present, request-line/headers are copied so the read buffer
//     can compact while reading the body into a pooled buffer.
//  4. SetBody keep-alive path caches a fully-encoded response when no custom
//     response headers are set.
//
// Scope: HTTP/1.1 server and client (Content-Length + chunked bodies),
// plus FS, compress, reuseport, prefork, and net/http adaptor helpers.
// No HTTP/2. TLS via ListenAndServeTLS / tls.NewListener (server) and
// Client.TLSConfig (client).
//
// Defaults (when fields are left zero): ReadTimeout/WriteTimeout 30s,
// IdleTimeout 90s, MaxRequestBodySize 4MiB, MaxHeaderBytes 8KiB,
// Concurrency 262144. Client MaxResponseHeaderBytes 64KiB.
// See README for limitations.
//
// Performance contract: rawhttp must never be slower than fasthttp on timed
// ServeConn gates, and must stay ≥2.35× on plaintext / ≥1.65× on JSON
// (enforced by test/TestGate_FasterThanFastHTTP_*) with 0 allocs/op on the
// hello path (TestAllocs_PlaintextHello). Client keep-alive comparison is
// informational (noisy pipe microbench; see test/gate_test.go).
package rawhttp

import (
	"errors"
	"sync"
	"time"
)

type Handler func(ctx *Ctx)

const (
	defaultBufSize        = 8192
	defaultMaxBodySize    = 4 << 20 // 4 MiB
	defaultMaxHeaderBytes = 8192
	defaultMaxHeaders     = 100
	maxMethodLen          = 32
	defaultReadTimeout    = 30 * time.Second
	defaultWriteTimeout   = 30 * time.Second
	defaultIdleTimeout    = 90 * time.Second
	defaultConcurrency    = 256 * 1024
)

var (
	errMalformedRequestLine = errors.New("rawhttp: malformed request line")
	errBufferFull           = errors.New("rawhttp: request line/headers exceed buffer size")
	ErrBodyTooLarge         = errors.New("rawhttp: request body too large")
	ErrURITooLong           = errors.New("rawhttp: request URI too long")
	errChunkedConflict      = errors.New("rawhttp: Content-Length with Transfer-Encoding")
	errBadChunk             = errors.New("rawhttp: malformed chunked body")
	ErrBadRequest           = errors.New("rawhttp: bad request")
	ErrMisdirectedRequest   = errors.New("rawhttp: misdirected request")
	errMissingHost          = errors.New("rawhttp: missing Host header")
	errInvalidContentLength = errors.New("rawhttp: invalid Content-Length")
	errDuplicateCL          = errors.New("rawhttp: duplicate Content-Length")
	errDuplicateHost        = errors.New("rawhttp: duplicate Host header")
	errExpectationFailed    = errors.New("rawhttp: expectation failed")
	ErrHeaderInvalid        = errors.New("rawhttp: response header contains CR/LF")
	ErrHeaderTooLarge       = errors.New("rawhttp: response headers too large")
	ErrServerClosed         = errors.New("rawhttp: server closed")
	ErrHijacked             = errors.New("rawhttp: connection already hijacked")
	ErrNotHijackable        = errors.New("rawhttp: connection not available for hijack")
	ErrRedirect             = errors.New("rawhttp: redirect rejected")
)

var ctxPool = sync.Pool{New: func() any {
	return &Ctx{
		respBuf: make([]byte, 0, 512),
		outBuf:  make([]byte, 0, 256),
		reqBody: make([]byte, 0, 512),
	}
}}

var readerBufPool = sync.Pool{New: func() any {
	b := make([]byte, defaultBufSize)
	return &b
}}

// ListenAndServe starts a server on addr with h.
func ListenAndServe(addr string, h Handler) error {
	s := &Server{Handler: h}
	return s.ListenAndServe(addr)
}

// ListenAndServeTLS starts a TLS server on addr with h.
func ListenAndServeTLS(addr, certFile, keyFile string, h Handler) error {
	s := &Server{Handler: h}
	return s.ListenAndServeTLS(addr, certFile, keyFile)
}
