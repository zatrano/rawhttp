// Package smuggling is a request-smuggling and framing corpus for rawhttp.
// The cases are plain data so the differential tests and the fuzz seeds can
// reuse them. The package has no dependencies.
package smuggling

// Case is one byte stream sent on a fresh connection.
type Case struct {
	Name string
	// Raw is written to the connection as-is, then the client half-closes.
	Raw string
	// Reject means the server must answer with a 4xx, run no handler, and
	// close the connection. The tests append a valid pipelined request to
	// every Reject case and check that it is not served either.
	Reject bool
	// Bodies lists the body of each request the handler must see, in order,
	// when Reject is false. Its length is the exact number of requests.
	Bodies []string
	// Ref names the rule the case exercises.
	Ref string
}

const after = "GET /after HTTP/1.1\r\nHost: localhost\r\n\r\n"

// Follow is the valid request appended to Reject cases.
const Follow = after

// bigTrailers is 20 trailer lines of 500 bytes: under the line and count
// limits, over the 8 KiB trailer-section cap.
func bigTrailers() string {
	val := make([]byte, 490)
	for i := range val {
		val[i] = 'v'
	}
	out := ""
	for i := 0; i < 20; i++ {
		out += "X-T" + string(rune('a'+i)) + ": " + string(val) + "\r\n"
	}
	return out
}

// Cases returns the corpus.
func Cases() []Case {
	return []Case{
		// Content-Length and Transfer-Encoding together (CL.TE / TE.CL).
		{Name: "cl-te", Reject: true, Ref: "RFC 9112 6.1", Raw: "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 6\r\nTransfer-Encoding: chunked\r\n\r\n0\r\n\r\nG"},
		{Name: "te-cl", Reject: true, Ref: "RFC 9112 6.1", Raw: "POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\nContent-Length: 3\r\n\r\n8\r\nSMUGGLED\r\n0\r\n\r\n"},
		{Name: "te-cl-zero", Reject: true, Ref: "RFC 9112 6.1", Raw: "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 0\r\nTransfer-Encoding: chunked\r\n\r\n0\r\n\r\n"},

		// Transfer-Encoding obfuscation.
		{Name: "te-duplicate", Reject: true, Ref: "RFC 9112 6.1", Raw: "POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\nTransfer-Encoding: chunked\r\n\r\n0\r\n\r\n"},
		{Name: "te-list-identity", Reject: true, Ref: "RFC 9112 6.1", Raw: "POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked, identity\r\n\r\n0\r\n\r\n"},
		{Name: "te-identity-first", Reject: true, Ref: "RFC 9112 6.1", Raw: "POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: identity, chunked\r\n\r\n0\r\n\r\n"},
		{Name: "te-xchunked", Reject: true, Ref: "RFC 9112 6.1", Raw: "POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: xchunked\r\n\r\n0\r\n\r\n"},
		{Name: "te-vertical-tab", Reject: true, Ref: "RFC 9110 5.5", Raw: "POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\x0b\r\n\r\n0\r\n\r\n"},
		{Name: "te-space-before-colon", Reject: true, Ref: "RFC 9112 5.1", Raw: "POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding : chunked\r\n\r\n0\r\n\r\n"},
		{Name: "te-obs-fold", Reject: true, Ref: "RFC 9112 5.2", Raw: "POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding:\r\n chunked\r\n\r\n0\r\n\r\n"},
		{Name: "te-http10", Reject: true, Ref: "RFC 9112 6.1", Raw: "POST / HTTP/1.0\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\n\r\n0\r\n\r\n"},
		{Name: "te-on-get", Reject: true, Ref: "rawhttp policy", Raw: "GET / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\n\r\n0\r\n\r\n"},
		{Name: "te-trailing-ows", Bodies: []string{""}, Ref: "RFC 9110 5.5", Raw: "POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked \r\nConnection: close\r\n\r\n0\r\n\r\n"},
		{Name: "te-uppercase", Bodies: []string{"hello"}, Ref: "RFC 9112 7", Raw: "POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: CHUNKED\r\nConnection: close\r\n\r\n5\r\nhello\r\n0\r\n\r\n"},

		// Content-Length shapes.
		{Name: "cl-duplicate-equal", Reject: true, Ref: "RFC 9112 6.3", Raw: "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 1\r\nContent-Length: 1\r\n\r\nx"},
		{Name: "cl-duplicate-different", Reject: true, Ref: "RFC 9112 6.3", Raw: "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 1\r\nContent-Length: 2\r\n\r\nxy"},
		{Name: "cl-list", Reject: true, Ref: "RFC 9112 6.3", Raw: "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 1, 1\r\n\r\nx"},
		{Name: "cl-plus", Reject: true, Ref: "RFC 9110 8.6", Raw: "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: +1\r\n\r\nx"},
		{Name: "cl-negative", Reject: true, Ref: "RFC 9110 8.6", Raw: "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: -1\r\n\r\nx"},
		{Name: "cl-leading-zero", Reject: true, Ref: "rawhttp policy", Raw: "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 01\r\n\r\nx"},
		{Name: "cl-hex", Reject: true, Ref: "RFC 9110 8.6", Raw: "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 0x1\r\n\r\nx"},
		{Name: "cl-inner-space", Reject: true, Ref: "RFC 9110 8.6", Raw: "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 1 1\r\n\r\nx"},
		{Name: "cl-overflow", Reject: true, Ref: "RFC 9110 8.6", Raw: "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 99999999999999999999999\r\n\r\nx"},
		{Name: "cl-empty", Reject: true, Ref: "RFC 9110 8.6", Raw: "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length:\r\n\r\nx"},
		{Name: "cl-on-get", Reject: true, Ref: "rawhttp policy", Raw: "GET / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 5\r\n\r\nhello"},
		{Name: "cl-hidden-request-in-body", Bodies: []string{"GET /admin HTTP/1.1\r\nHost: localhost\r\n\r\n"}, Ref: "RFC 9112 6.3", Raw: "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 40\r\nConnection: close\r\n\r\nGET /admin HTTP/1.1\r\nHost: localhost\r\n\r\n"},
		{Name: "cl-ows", Bodies: []string{"x"}, Ref: "RFC 9110 5.5", Raw: "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length:  1 \r\nConnection: close\r\n\r\nx"},

		// Chunked framing.
		{Name: "chunk-size-leading-space", Reject: true, Ref: "RFC 9112 7.1", Raw: "POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\n\r\n 5\r\nhello\r\n0\r\n\r\n"},
		{Name: "chunk-size-0x", Reject: true, Ref: "RFC 9112 7.1", Raw: "POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\n\r\n0x5\r\nhello\r\n0\r\n\r\n"},
		{Name: "chunk-size-negative", Reject: true, Ref: "RFC 9112 7.1", Raw: "POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\n\r\n-5\r\nhello\r\n0\r\n\r\n"},
		{Name: "chunk-size-overflow", Reject: true, Ref: "RFC 9112 7.1", Raw: "POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\n\r\n10000000000000005\r\nhello\r\n0\r\n\r\n"},
		{Name: "chunk-size-bare-lf", Reject: true, Ref: "RFC 9112 2.2", Raw: "POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\n\r\n5\nhello\r\n0\r\n\r\n"},
		{Name: "chunk-data-missing-crlf", Reject: true, Ref: "RFC 9112 7.1", Raw: "POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nhelloXX0\r\n\r\n"},
		{Name: "chunk-data-too-long", Reject: true, Ref: "RFC 9112 7.1", Raw: "POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\n\r\n3\r\nhello\r\n0\r\n\r\n"},
		{Name: "chunk-ext-bare-cr", Reject: true, Ref: "RFC 9112 7.1.1", Raw: "POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\n\r\n5;a=\rb\r\nhello\r\n0\r\n\r\n"},
		{Name: "chunk-trailer-content-length", Reject: true, Ref: "RFC 9110 6.5.1", Raw: "POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\n\r\n0\r\nContent-Length: 5\r\n\r\n"},
		{Name: "chunk-trailer-transfer-encoding", Reject: true, Ref: "RFC 9110 6.5.1", Raw: "POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\n\r\n0\r\nTransfer-Encoding: chunked\r\n\r\n"},
		{Name: "chunk-trailer-obs-fold", Reject: true, Ref: "RFC 9112 5.2", Raw: "POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\n\r\n0\r\nX-T: a\r\n b\r\n\r\n"},
		{Name: "chunk-ext-bws", Reject: true, Ref: "RFC 9112 7.1.1 / net/http", Raw: "POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\n\r\n5 ;ext=1\r\nhello\r\n0\r\n\r\n"},
		{Name: "chunk-hidden-request", Bodies: []string{"0\r\n\r\nGET /admin HTTP/1.1\r\n\r\n"}, Ref: "RFC 9112 7.1", Raw: "POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n1c\r\n0\r\n\r\nGET /admin HTTP/1.1\r\n\r\n\r\n0\r\n\r\n"},
		{Name: "chunk-trailer-allowed", Bodies: []string{"hi"}, Ref: "RFC 9112 7.1.2", Raw: "POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n2\r\nhi\r\n0\r\nX-Checksum: 1\r\n\r\n"},

		// Line endings and header syntax.
		{Name: "bare-lf-request", Reject: true, Ref: "RFC 9112 2.2", Raw: "GET / HTTP/1.1\nHost: localhost\n\n"},
		{Name: "bare-cr-header-value", Reject: true, Ref: "RFC 9112 2.2", Raw: "GET / HTTP/1.1\r\nHost: localhost\r\nX-A: a\rb\r\n\r\n"},
		{Name: "obs-fold", Reject: true, Ref: "RFC 9112 5.2", Raw: "GET / HTTP/1.1\r\nHost: localhost\r\nX-A: 1\r\n 2\r\n\r\n"},
		{Name: "header-nul-name", Reject: true, Ref: "RFC 9110 5.1", Raw: "GET / HTTP/1.1\r\nHost: localhost\r\nX\x00A: 1\r\n\r\n"},
		{Name: "header-nul-value", Reject: true, Ref: "RFC 9110 5.5", Raw: "GET / HTTP/1.1\r\nHost: localhost\r\nX-A: 1\x002\r\n\r\n"},
		{Name: "header-empty-name", Reject: true, Ref: "RFC 9110 5.1", Raw: "GET / HTTP/1.1\r\nHost: localhost\r\n: x\r\n\r\n"},
		{Name: "header-no-colon", Reject: true, Ref: "RFC 9112 5", Raw: "GET / HTTP/1.1\r\nHost: localhost\r\nX-A\r\n\r\n"},
		{Name: "header-high-byte-name", Reject: true, Ref: "RFC 9110 5.1", Raw: "GET / HTTP/1.1\r\nHost: localhost\r\nContent-Lengt\xc4\xb1: 5\r\n\r\nhello"},
		{Name: "leading-empty-line", Reject: true, Ref: "rawhttp policy", Raw: "\r\nGET / HTTP/1.1\r\nHost: localhost\r\n\r\n"},
		{Name: "proxy-connection", Reject: true, Ref: "rawhttp policy", Raw: "GET / HTTP/1.1\r\nHost: localhost\r\nProxy-Connection: keep-alive\r\n\r\n"},
		{Name: "te-header-hop", Reject: true, Ref: "rawhttp policy", Raw: "GET / HTTP/1.1\r\nHost: localhost\r\nTE: trailers\r\n\r\n"},
		{Name: "upgrade-not-allowed", Reject: true, Ref: "rawhttp policy", Raw: "GET / HTTP/1.1\r\nHost: localhost\r\nConnection: Upgrade\r\nUpgrade: h2c\r\n\r\n"},
		{Name: "expect-on-get", Reject: true, Ref: "RFC 9110 10.1.1", Raw: "GET / HTTP/1.1\r\nHost: localhost\r\nExpect: 100-continue\r\n\r\n"},

		// Request line.
		{Name: "request-line-tab", Reject: true, Ref: "RFC 9112 3", Raw: "GET\t/ HTTP/1.1\r\nHost: localhost\r\n\r\n"},
		{Name: "request-line-double-space", Reject: true, Ref: "RFC 9112 3", Raw: "GET  / HTTP/1.1\r\nHost: localhost\r\n\r\n"},
		{Name: "request-line-trailing-space", Reject: true, Ref: "RFC 9112 3", Raw: "GET / HTTP/1.1 \r\nHost: localhost\r\n\r\n"},
		{Name: "absolute-form", Reject: true, Ref: "rawhttp policy", Raw: "GET http://evil.example/ HTTP/1.1\r\nHost: localhost\r\n\r\n"},
		{Name: "scheme-relative", Reject: true, Ref: "rawhttp policy", Raw: "GET //evil.example/ HTTP/1.1\r\nHost: localhost\r\n\r\n"},
		{Name: "version-1.2", Reject: true, Ref: "RFC 9112 2.3", Raw: "GET / HTTP/1.2\r\nHost: localhost\r\n\r\n"},
		{Name: "version-lowercase", Reject: true, Ref: "RFC 9112 2.3", Raw: "GET / http/1.1\r\nHost: localhost\r\n\r\n"},
		{Name: "http09", Reject: true, Ref: "RFC 9112 2.3", Raw: "GET /\r\n\r\n"},
		{Name: "dotdot", Reject: true, Ref: "rawhttp policy", Raw: "GET /a/../b HTTP/1.1\r\nHost: localhost\r\n\r\n"},
		{Name: "trace", Reject: true, Ref: "rawhttp policy", Raw: "TRACE / HTTP/1.1\r\nHost: localhost\r\n\r\n"},
		{Name: "connect", Reject: true, Ref: "rawhttp policy", Raw: "CONNECT localhost:443 HTTP/1.1\r\nHost: localhost\r\n\r\n"},

		// Host.
		{Name: "host-missing", Reject: true, Ref: "RFC 9112 3.2", Raw: "GET / HTTP/1.1\r\n\r\n"},
		{Name: "host-duplicate", Reject: true, Ref: "RFC 9112 3.2", Raw: "GET / HTTP/1.1\r\nHost: a\r\nHost: b\r\n\r\n"},
		{Name: "host-userinfo", Reject: true, Ref: "RFC 9112 3.2", Raw: "GET / HTTP/1.1\r\nHost: user@evil\r\n\r\n"},
		{Name: "host-comma", Reject: true, Ref: "RFC 9112 3.2", Raw: "GET / HTTP/1.1\r\nHost: a,b\r\n\r\n"},
		{Name: "host-space", Reject: true, Ref: "RFC 9112 3.2", Raw: "GET / HTTP/1.1\r\nHost: a b\r\n\r\n"},
		{Name: "host-port-not-numeric", Reject: true, Ref: "RFC 9110 7.2", Raw: "GET / HTTP/1.1\r\nHost: app.local:evil\r\n\r\n"},
		{Name: "host-two-ports", Reject: true, Ref: "RFC 9110 7.2", Raw: "GET / HTTP/1.1\r\nHost: app.local:80:81\r\n\r\n"},
		{Name: "host-query", Reject: true, Ref: "RFC 9110 7.2", Raw: "GET / HTTP/1.1\r\nHost: evil.example?x=1\r\n\r\n"},
		{Name: "host-fragment", Reject: true, Ref: "RFC 9110 7.2", Raw: "GET / HTTP/1.1\r\nHost: evil.example#x\r\n\r\n"},
		{Name: "host-percent", Reject: true, Ref: "rawhttp policy", Raw: "GET / HTTP/1.1\r\nHost: evil%2eexample\r\n\r\n"},
		{Name: "host-ipv6-unclosed", Reject: true, Ref: "RFC 3986 3.2.2", Raw: "GET / HTTP/1.1\r\nHost: [::1\r\n\r\n"},
		{Name: "host-port-too-long", Reject: true, Ref: "RFC 9110 7.2", Raw: "GET / HTTP/1.1\r\nHost: a:123456\r\n\r\n"},
		{Name: "host-ipv6-port", Bodies: []string{""}, Ref: "RFC 3986 3.2.2", Raw: "GET / HTTP/1.1\r\nHost: [::1]:8080\r\nConnection: close\r\n\r\n"},
		{Name: "host-name-port", Bodies: []string{""}, Ref: "RFC 9110 7.2", Raw: "GET / HTTP/1.1\r\nHost: app.example:8443\r\nConnection: close\r\n\r\n"},

		// Singleton fields and expectations.
		{Name: "content-type-duplicate", Reject: true, Ref: "RFC 9110 5.3", Raw: "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/json\r\nContent-Type: application/octet-stream\r\nContent-Length: 2\r\n\r\n{}"},
		{Name: "authorization-duplicate", Reject: true, Ref: "RFC 9110 5.3", Raw: "GET / HTTP/1.1\r\nHost: localhost\r\nAuthorization: Bearer a\r\nAuthorization: Bearer b\r\n\r\n"},
		{Name: "origin-duplicate", Reject: true, Ref: "RFC 6454 7.2", Raw: "GET / HTTP/1.1\r\nHost: localhost\r\nOrigin: https://evil.example\r\nOrigin: https://app.example\r\n\r\n"},
		{Name: "expect-list-unknown", Reject: true, Ref: "RFC 9110 10.1.1", Raw: "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 1\r\nExpect: 100-continue, evil\r\n\r\nx"},
		{Name: "expect-garbage-suffix", Reject: true, Ref: "RFC 9110 10.1.1", Raw: "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 1\r\nExpect: 100-continue garbage\r\n\r\nx"},
		{Name: "trailer-section-too-large", Reject: true, Ref: "rawhttp policy", Raw: "POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\n\r\n0\r\n" + bigTrailers() + "\r\n"},

		// Valid streams.
		{Name: "pipelined-two", Bodies: []string{"", "ab"}, Ref: "RFC 9112 9.3.2", Raw: "GET /1 HTTP/1.1\r\nHost: localhost\r\n\r\nPOST /2 HTTP/1.1\r\nHost: localhost\r\nContent-Length: 2\r\nConnection: close\r\n\r\nab"},
		{Name: "connection-close-stops", Bodies: []string{""}, Ref: "RFC 9112 9.6", Raw: "GET /1 HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\nGET /2 HTTP/1.1\r\nHost: localhost\r\n\r\n"},
		{Name: "http10-closes", Bodies: []string{""}, Ref: "RFC 9112 9.3", Raw: "GET /1 HTTP/1.0\r\n\r\nGET /2 HTTP/1.0\r\n\r\n"},
		{Name: "unknown-te-like-header-is-not-framing", Bodies: []string{""}, Ref: "RFC 9110 5.1", Raw: "POST / HTTP/1.1\r\nHost: localhost\r\nTransfer_Encoding: chunked\r\nConnection: close\r\n\r\n"},
	}
}
