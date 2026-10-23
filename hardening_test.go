package rawhttp_test

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/zatrano/rawhttp"
)

func serveRaw(t *testing.T, srv *rawhttp.Server, raw string) string {
	t.Helper()
	fc := newFakeConn([]byte(raw))
	_ = srv.ServeConn(fc)
	return fc.w.String()
}

func readAllResponses(t *testing.T, wire string) []*http.Response {
	t.Helper()
	br := bufio.NewReader(strings.NewReader(wire))
	var out []*http.Response
	for {
		resp, err := http.ReadResponse(br, nil)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return out
			}
			t.Fatalf("response %d does not parse: %v\nwire=%q", len(out), err, wire)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		out = append(out, resp)
	}
}

// Content-Length and Transfer-Encoding come from the engine only, so a
// handler cannot make a response whose framing a proxy reads differently.
func TestSetHeaderRejectsFramingHeaders(t *testing.T) {
	var errs []error
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) {
		errs = append(errs,
			ctx.SetHeader("Content-Length", "1"),
			ctx.SetHeader("content-length", "100"),
			ctx.SetHeader("Transfer-Encoding", "chunked"),
			ctx.SetHeaderBytes("Content-Length", []byte("3")),
			ctx.SetHeaderBytes("TRANSFER-ENCODING", []byte("chunked")),
		)
		ctx.SetBodyString("hello")
	}}
	wire := serveRaw(t, srv, "GET / HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n")
	for i, err := range errs {
		if !errors.Is(err, rawhttp.ErrHeaderInvalid) {
			t.Fatalf("call %d: err=%v, want ErrHeaderInvalid", i, err)
		}
	}
	head := wire[:strings.Index(wire, "\r\n\r\n")]
	if strings.Count(strings.ToLower(head), "content-length:") != 1 || strings.Contains(strings.ToLower(head), "transfer-encoding") {
		t.Fatalf("framing headers leaked into the response:\n%s", head)
	}
	resps := readAllResponses(t, wire)
	if len(resps) != 1 || resps[0].ContentLength != 5 {
		t.Fatalf("responses=%d", len(resps))
	}
}

func TestSetHeaderConnectionCloseClosesConnection(t *testing.T) {
	served := 0
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) {
		served++
		if err := ctx.SetHeader("Connection", "close"); err != nil {
			t.Errorf("SetHeader(Connection, close): %v", err)
		}
		ctx.SetBodyString("ok")
	}}
	wire := serveRaw(t, srv, "GET /1 HTTP/1.1\r\nHost: x\r\n\r\nGET /2 HTTP/1.1\r\nHost: x\r\n\r\n")
	if served != 1 {
		t.Fatalf("served %d requests after Connection: close", served)
	}
	head := wire[:strings.Index(wire, "\r\n\r\n")]
	if strings.Count(head, "Connection:") != 1 || !strings.Contains(head, "Connection: close") {
		t.Fatalf("want exactly one Connection: close:\n%s", head)
	}
}

func TestSetHeaderConnectionKeepAliveIsIgnored(t *testing.T) {
	served := 0
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) {
		served++
		if err := ctx.SetHeader("Connection", "keep-alive"); err != nil {
			t.Errorf("SetHeader(Connection, keep-alive): %v", err)
		}
		ctx.SetBodyString("ok")
	}}
	wire := serveRaw(t, srv, "GET /1 HTTP/1.1\r\nHost: x\r\n\r\nGET /2 HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n")
	if served != 2 {
		t.Fatalf("served %d", served)
	}
	resps := readAllResponses(t, wire)
	if len(resps) != 2 || len(resps[0].Header.Values("Connection")) != 0 {
		t.Fatalf("keep-alive response carried a handler Connection header: %v", resps[0].Header)
	}
}

func TestSetCookieRejectsAttributeInjection(t *testing.T) {
	var errs []error
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) {
		errs = append(errs,
			ctx.SetCookie(&rawhttp.Cookie{Name: "a", Value: "x; Domain=evil.example"}),
			ctx.SetCookie(&rawhttp.Cookie{Name: "b", Value: "x", Path: "/; HttpOnly=false"}),
			ctx.SetCookie(&rawhttp.Cookie{Name: "c", Value: "x", Domain: "a.example; Secure"}),
		)
		_ = ctx.SetCookie(&rawhttp.Cookie{Name: "ok", Value: "v", Path: "/"})
		ctx.SetBodyString("ok")
	}}
	wire := serveRaw(t, srv, "GET / HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n")
	for i, err := range errs {
		if !errors.Is(err, rawhttp.ErrHeaderInvalid) {
			t.Fatalf("cookie %d: err=%v", i, err)
		}
	}
	if strings.Contains(wire, "evil.example") || strings.Count(wire, "Set-Cookie:") != 1 {
		t.Fatalf("injected cookie attributes reached the wire:\n%s", wire)
	}
}

// After a handler panic the request state is unknown, so the 500 closes the
// connection and a pipelined request is not served.
func TestPanicClosesConnection(t *testing.T) {
	served := 0
	srv := &rawhttp.Server{
		ErrorLog: log.New(io.Discard, "", 0),
		Handler: func(ctx *rawhttp.Ctx) {
			served++
			ctx.SetBodyStream(strings.NewReader("stream-before-panic"), -1)
			panic("boom")
		},
	}
	wire := serveRaw(t, srv, "GET /1 HTTP/1.1\r\nHost: x\r\n\r\nGET /2 HTTP/1.1\r\nHost: x\r\n\r\n")
	if served != 1 {
		t.Fatalf("served %d requests after a panic", served)
	}
	resps := readAllResponses(t, wire)
	if len(resps) != 1 || resps[0].StatusCode != 500 || !resps[0].Close {
		t.Fatalf("want one closing 500, got %d responses: %q", len(resps), wire)
	}
	if strings.Contains(wire, "stream-before-panic") {
		t.Fatal("the stream body set before the panic was written")
	}
}

func TestHTTP10TransferEncodingRejected(t *testing.T) {
	served := 0
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) { served++ }}
	wire := serveRaw(t, srv, "POST / HTTP/1.0\r\nTransfer-Encoding: chunked\r\n\r\n0\r\n\r\n")
	if served != 0 || !strings.HasPrefix(wire, "HTTP/1.1 400") {
		t.Fatalf("served=%d wire=%q", served, wire)
	}
}

func TestClientIPIgnoresSpoofedLeftHops(t *testing.T) {
	cases := []struct {
		name    string
		trusted []string
		headers string
		want    string
	}{
		{"untrusted peer ignores XFF", []string{"10.0.0.0/8"}, "X-Forwarded-For: 203.0.113.1\r\n", "127.0.0.1"},
		{"spoofed left hop", []string{"127.0.0.0/8"}, "X-Forwarded-For: 1.2.3.4, 198.51.100.7\r\n", "198.51.100.7"},
		{"trusted chain", []string{"127.0.0.0/8", "10.0.0.0/8"}, "X-Forwarded-For: 1.2.3.4, 198.51.100.7, 10.1.1.1\r\n", "198.51.100.7"},
		{"split header lines", []string{"127.0.0.0/8"}, "X-Forwarded-For: 1.2.3.4\r\nX-Forwarded-For: 198.51.100.8\r\n", "198.51.100.8"},
		{"garbage hop stops the walk", []string{"127.0.0.0/8"}, "X-Forwarded-For: 1.2.3.4, not-an-ip\r\n", "127.0.0.1"},
		{"all trusted returns leftmost", []string{"127.0.0.0/8", "10.0.0.0/8"}, "X-Forwarded-For: 10.0.0.9, 10.0.0.8\r\n", "10.0.0.9"},
		{"x-real-ip from trusted peer", []string{"127.0.0.0/8"}, "X-Real-IP: 198.51.100.9\r\n", "198.51.100.9"},
		{"x-real-ip garbage", []string{"127.0.0.0/8"}, "X-Real-IP: <script>\r\n", "127.0.0.1"},
		{"ipv6 hop", []string{"127.0.0.0/8"}, "X-Forwarded-For: [2001:db8::1]:443\r\n", "2001:db8::1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got string
			srv := &rawhttp.Server{TrustedProxies: tc.trusted, Handler: func(ctx *rawhttp.Ctx) { got = ctx.ClientIP() }}
			serveRaw(t, srv, "GET / HTTP/1.1\r\nHost: x\r\n"+tc.headers+"Connection: close\r\n\r\n")
			if got != tc.want {
				t.Fatalf("ClientIP=%q want %q", got, tc.want)
			}
		})
	}
}

func selfSignedCert(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		DNSNames:     []string{"localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func TestServerTLSConfigRefusesOldVersionsAndWeakCiphers(t *testing.T) {
	cfg := rawhttp.ServerTLSConfig(selfSignedCert(t))
	if cfg.MinVersion != tls.VersionTLS12 {
		t.Fatalf("MinVersion=%x", cfg.MinVersion)
	}
	for _, id := range cfg.CipherSuites {
		name := tls.CipherSuiteName(id)
		if !strings.HasPrefix(name, "TLS_ECDHE_") || !(strings.Contains(name, "GCM") || strings.Contains(name, "CHACHA20")) {
			t.Fatalf("weak cipher suite %s", name)
		}
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) { ctx.SetBodyString("ok") }}
	go func() { _ = srv.Serve(tls.NewListener(ln, cfg)) }()
	t.Cleanup(func() { _ = srv.Close() })
	addr := ln.Addr().String()

	dial := func(c *tls.Config) error {
		c.InsecureSkipVerify = true //nolint:gosec // self-signed test certificate
		conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", addr, c)
		if err != nil {
			return err
		}
		return conn.Close()
	}
	if err := dial(&tls.Config{MaxVersion: tls.VersionTLS11}); err == nil { //nolint:gosec // the handshake must fail
		t.Fatal("TLS 1.1 handshake succeeded")
	}
	weak := &tls.Config{
		MaxVersion:   tls.VersionTLS12,
		CipherSuites: []uint16{tls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA, tls.TLS_RSA_WITH_AES_128_GCM_SHA256}, //nolint:gosec // the handshake must fail
	}
	if err := dial(weak); err == nil {
		t.Fatal("TLS 1.2 handshake with only CBC / RSA key exchange succeeded")
	}
	if err := dial(&tls.Config{MinVersion: tls.VersionTLS12}); err != nil {
		t.Fatalf("modern handshake failed: %v", err)
	}
}
