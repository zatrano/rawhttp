package test_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zatrano/rawhttp"
)

func TestRequestBodyContentLength(t *testing.T) {
	req := "POST /echo HTTP/1.1\r\nHost: localhost\r\nContent-Length: 5\r\nConnection: close\r\n\r\nhello"
	var gotBody, gotPath string
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) {
		gotPath = string(ctx.Path)
		gotBody = string(ctx.Body())
		ctx.Write(ctx.Body())
	}}
	fc := newFakeConn([]byte(req))
	if err := srv.ServeConn(fc); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/echo" || gotBody != "hello" {
		t.Fatalf("path=%q body=%q", gotPath, gotBody)
	}
	if !strings.HasSuffix(fc.w.String(), "hello") {
		t.Fatalf("response %q", fc.w.String())
	}
}

func TestRequestBodyChunked(t *testing.T) {
	req := "POST /c HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n" +
		"5\r\nhello\r\n" +
		"6\r\n world\r\n" +
		"0\r\n\r\n"
	var got string
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) {
		got = string(ctx.Body())
		ctx.SetBody(ctx.Body())
	}}
	fc := newFakeConn([]byte(req))
	if err := srv.ServeConn(fc); err != nil {
		t.Fatal(err)
	}
	if got != "hello world" {
		t.Fatalf("got %q", got)
	}
}

func TestMaxRequestBodySize(t *testing.T) {
	req := "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 10\r\nConnection: close\r\n\r\n0123456789"
	srv := &rawhttp.Server{
		MaxRequestBodySize: 5,
		Handler:            func(ctx *rawhttp.Ctx) { t.Fatal("handler should not run") },
	}
	fc := newFakeConn([]byte(req))
	err := srv.ServeConn(fc)
	if err != rawhttp.ErrBodyTooLarge {
		t.Fatalf("got err %v", err)
	}
	if !strings.Contains(fc.w.String(), "413") {
		t.Fatalf("expected 413, got %q", fc.w.String())
	}
}

func TestCustomResponseHeaders(t *testing.T) {
	req := "GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) {
		ctx.SetStatusCode(201)
		ctx.SetContentType("application/json")
		if err := ctx.SetHeader("X-Req-Id", "abc"); err != nil {
			t.Fatal(err)
		}
		ctx.WriteString(`{"ok":true}`)
	}}
	fc := newFakeConn([]byte(req))
	if err := srv.ServeConn(fc); err != nil {
		t.Fatal(err)
	}
	resp := fc.w.String()
	if !strings.Contains(resp, "201 Created") {
		t.Fatalf("status: %q", resp)
	}
	if !strings.Contains(resp, "Content-Type: application/json\r\n") {
		t.Fatalf("ct: %q", resp)
	}
	if !strings.Contains(resp, "X-Req-Id: abc\r\n") {
		t.Fatalf("hdr: %q", resp)
	}
	if !strings.HasSuffix(resp, `{"ok":true}`) {
		t.Fatalf("body: %q", resp)
	}
}

func TestDefaultDateHeader(t *testing.T) {
	req := "GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) {
		ctx.SetBody([]byte("ok"))
	}}
	fc := newFakeConn([]byte(req))
	if err := srv.ServeConn(fc); err != nil {
		t.Fatal(err)
	}
	resp := fc.w.String()
	if !strings.Contains(resp, "\r\nDate: ") || !strings.Contains(resp, " GMT\r\n") {
		t.Fatalf("missing Date header: %q", resp)
	}
}

func TestNoDefaultDate(t *testing.T) {
	req := "GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
	srv := &rawhttp.Server{
		NoDefaultDate: true,
		Handler:       func(ctx *rawhttp.Ctx) { ctx.SetBody([]byte("ok")) },
	}
	fc := newFakeConn([]byte(req))
	if err := srv.ServeConn(fc); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(fc.w.String(), "\r\nDate: ") {
		t.Fatalf("Date should be omitted: %q", fc.w.String())
	}
}

func TestNoDefaultContentType(t *testing.T) {
	req := "GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
	srv := &rawhttp.Server{
		NoDefaultContentType: true,
		Handler:              func(ctx *rawhttp.Ctx) { ctx.SetBody([]byte("ok")) },
	}
	fc := newFakeConn([]byte(req))
	if err := srv.ServeConn(fc); err != nil {
		t.Fatal(err)
	}
	resp := fc.w.String()
	if strings.Contains(resp, "Content-Type:") {
		t.Fatalf("Content-Type should be omitted: %q", resp)
	}
	if !strings.Contains(resp, "ok") {
		t.Fatalf("want body: %q", resp)
	}
}

func TestConnState(t *testing.T) {
	var mu sync.Mutex
	var states []rawhttp.ConnState
	srv := &rawhttp.Server{
		ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
		Handler: func(ctx *rawhttp.Ctx) { ctx.SetBody([]byte("ok")) },
		ConnState: func(_ net.Conn, st rawhttp.ConnState) {
			mu.Lock()
			states = append(states, st)
			mu.Unlock()
		},
	}
	fc := newFakeConn([]byte("GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"))
	_ = srv.ServeConn(fc)
	mu.Lock()
	defer mu.Unlock()
	if len(states) < 2 {
		t.Fatalf("want Idle+Active at least, got %v", states)
	}
	sawIdle, sawActive := false, false
	for _, st := range states {
		if st == rawhttp.StateIdle {
			sawIdle = true
		}
		if st == rawhttp.StateActive {
			sawActive = true
		}
	}
	if !sawIdle || !sawActive {
		t.Fatalf("states=%v", states)
	}
}

func TestIndexedRequestHeaders(t *testing.T) {
	req := "GET / HTTP/1.1\r\nHost: example.com\r\nUser-Agent: raw-test\r\nAccept: text/plain\r\nContent-Type: application/json\r\nConnection: close\r\n\r\n"
	var host, ua, accept, ct string
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) {
		host = string(ctx.Host())
		ua = string(ctx.UserAgent())
		accept = string(ctx.Header("Accept"))
		ct = string(ctx.RequestContentType())
		ctx.SetBody([]byte("ok"))
	}}
	fc := newFakeConn([]byte(req))
	if err := srv.ServeConn(fc); err != nil {
		t.Fatal(err)
	}
	if host != "example.com" || ua != "raw-test" || accept != "text/plain" || ct != "application/json" {
		t.Fatalf("host=%q ua=%q accept=%q ct=%q", host, ua, accept, ct)
	}
}

func TestQueryArgs(t *testing.T) {
	req := "GET /search?q=golang&page=2 HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
	var path, q, page string
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) {
		path = string(ctx.Path)
		q = string(ctx.QueryArgs().Get("q"))
		page = string(ctx.QueryArgs().Get("page"))
	}}
	fc := newFakeConn([]byte(req))
	if err := srv.ServeConn(fc); err != nil {
		t.Fatal(err)
	}
	if path != "/search" || q != "golang" || page != "2" {
		t.Fatalf("path=%q q=%q page=%q", path, q, page)
	}
}

func TestPanicRecoversWith500(t *testing.T) {
	req := "GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
	srv := &rawhttp.Server{
		ErrorLog: log.New(io.Discard, "", 0),
		Handler:  func(ctx *rawhttp.Ctx) { panic("boom") },
	}
	fc := newFakeConn([]byte(req))
	if err := srv.ServeConn(fc); err != nil {
		t.Fatal(err)
	}
	resp := fc.w.String()
	if !strings.Contains(resp, "500") {
		t.Fatalf("expected 500, got %q", resp)
	}
	if !strings.Contains(resp, "Internal Server Error") {
		t.Fatalf("body: %q", resp)
	}
}

func TestShutdown(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) {
		ctx.WriteString("ok")
	}}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	_, _ = conn.Write([]byte("GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"))
	buf := make([]byte, 256)
	n, _ := conn.Read(buf)
	_ = conn.Close()
	if !strings.Contains(string(buf[:n]), "ok") {
		t.Fatalf("resp %q", buf[:n])
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-errCh:
		if err != nil && err != rawhttp.ErrServerClosed && !strings.Contains(err.Error(), "closed") {
			t.Fatalf("serve err: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return")
	}
}

func TestServeTLS(t *testing.T) {
	certPEM, keyPEM := mustTestCert(t)
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tlsLn := tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{cert}})
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) {
		ctx.WriteString("secure")
	}}
	go func() { _ = srv.Serve(tlsLn) }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
		Timeout: 2 * time.Second,
	}
	resp, err := client.Get("https://" + ln.Addr().String() + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "secure" {
		t.Fatalf("got %q", body)
	}
}

func mustTestCert(t *testing.T) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM
}

func TestPipelinedPOSTThenGET(t *testing.T) {
	req := "POST /a HTTP/1.1\r\nHost: localhost\r\nContent-Length: 3\r\n\r\nabc" +
		"GET /b?x=1 HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
	var bodies, paths []string
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) {
		paths = append(paths, string(ctx.Path))
		bodies = append(bodies, string(ctx.Body()))
	}}
	fc := newFakeConn([]byte(req))
	if err := srv.ServeConn(fc); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 || paths[0] != "/a" || paths[1] != "/b" {
		t.Fatalf("paths %v", paths)
	}
	if bodies[0] != "abc" || bodies[1] != "" {
		t.Fatalf("bodies %v", bodies)
	}
}

func TestDisablePipeliningClosesOnLeftover(t *testing.T) {
	req := "GET /a HTTP/1.1\r\nHost: localhost\r\n\r\n" +
		"GET /b HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
	n := 0
	srv := &rawhttp.Server{
		DisablePipelining: true,
		Handler: func(ctx *rawhttp.Ctx) {
			n++
			ctx.SetBody([]byte(ctx.Path))
		},
	}
	fc := newFakeConn([]byte(req))
	_ = srv.ServeConn(fc)
	if n != 1 {
		t.Fatalf("handled %d requests, want 1", n)
	}
	resp := fc.w.String()
	if strings.Count(resp, "HTTP/1.1") != 1 {
		t.Fatalf("want single response, got %q", resp)
	}
	if !strings.Contains(resp, "Connection: close") {
		t.Fatalf("want Connection: close, got %q", resp)
	}
}

func TestRequireTLS(t *testing.T) {
	srv := &rawhttp.Server{
		RequireTLS:  true,
		Handler:     func(ctx *rawhttp.Ctx) { ctx.SetBody([]byte("ok")) },
		ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
	}
	fc := newFakeConn([]byte("GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "403") {
		t.Fatalf("want 403: %q", fc.w.String())
	}
}

func TestAppendUint(t *testing.T) {
	srv := &rawhttp.Server{
		Handler: func(ctx *rawhttp.Ctx) {
			ctx.AppendBodyString("n=")
			ctx.AppendUint(42)
		},
		ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
	}
	fc := newFakeConn([]byte("GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "n=42") {
		t.Fatalf("got %q", fc.w.String())
	}
}

func TestTimeoutError(t *testing.T) {
	srv := &rawhttp.Server{
		Handler: func(ctx *rawhttp.Ctx) {
			ctx.TimeoutError("slow")
			ctx.SetBody([]byte("ignored"))
		},
		ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
	}
	fc := newFakeConn([]byte("GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"))
	_ = srv.ServeConn(fc)
	resp := fc.w.String()
	if !strings.Contains(resp, "503") || !strings.Contains(resp, "slow") {
		t.Fatalf("want 503 slow: %q", resp)
	}
	if strings.Contains(resp, "ignored") {
		t.Fatalf("body should be ignored: %q", resp)
	}
}

func TestGetOnly(t *testing.T) {
	srv := &rawhttp.Server{
		GetOnly: true,
		Handler: func(ctx *rawhttp.Ctx) { ctx.SetBody([]byte("ok")) },
	}
	fc := newFakeConn([]byte("POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "405") {
		t.Fatalf("want 405: %q", fc.w.String())
	}
	fc = newFakeConn([]byte("GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "ok") {
		t.Fatalf("GET should pass: %q", fc.w.String())
	}
}

func TestMaxConnDuration(t *testing.T) {
	srv := &rawhttp.Server{
		MaxConnDuration: time.Millisecond,
		Handler:         func(ctx *rawhttp.Ctx) { ctx.SetBody([]byte("x")) },
		ReadTimeout:     -1, WriteTimeout: -1, IdleTimeout: -1,
	}
	// Two keep-alive requests; after first response duration has elapsed → close.
	req := "GET /a HTTP/1.1\r\nHost: localhost\r\n\r\n" +
		"GET /b HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
	time.Sleep(2 * time.Millisecond)
	fc := newFakeConn([]byte(req))
	_ = srv.ServeConn(fc)
	resp := fc.w.String()
	if strings.Count(resp, "HTTP/1.1 200") < 1 {
		t.Fatalf("want at least one 200: %q", resp)
	}
	if !strings.Contains(resp, "Connection: close") {
		t.Fatalf("want close after MaxConnDuration: %q", resp)
	}
}

func TestMissingHostReturns400(t *testing.T) {
	req := "GET / HTTP/1.1\r\nConnection: close\r\n\r\n"
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) { t.Fatal("handler") }}
	fc := newFakeConn([]byte(req))
	err := srv.ServeConn(fc)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(fc.w.String(), "400") {
		t.Fatalf("expected 400, got %q", fc.w.String())
	}
}

func TestDuplicateContentLengthReturns400(t *testing.T) {
	req := "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 1\r\nContent-Length: 2\r\nConnection: close\r\n\r\nx"
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) { t.Fatal("handler") }}
	fc := newFakeConn([]byte(req))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "400") {
		t.Fatalf("expected 400, got %q", fc.w.String())
	}
}

func TestChunkedWithContentLengthReturns400(t *testing.T) {
	req := "POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\nContent-Length: 5\r\nConnection: close\r\n\r\n0\r\n\r\n"
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) { t.Fatal("handler") }}
	fc := newFakeConn([]byte(req))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "400") {
		t.Fatalf("expected 400, got %q", fc.w.String())
	}
}

func TestContentLengthOverflowReturns400(t *testing.T) {
	req := "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 999999999999999999999\r\nConnection: close\r\n\r\n"
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) { t.Fatal("handler") }}
	fc := newFakeConn([]byte(req))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "400") {
		t.Fatalf("expected 400, got %q", fc.w.String())
	}
}

func TestSetHeaderRejectsCRLF(t *testing.T) {
	req := "GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) {
		err := ctx.SetHeader("X-Evil", "a\r\nInjected: yes")
		if err != rawhttp.ErrHeaderInvalid {
			t.Fatalf("got %v", err)
		}
		ctx.WriteString("ok")
	}}
	fc := newFakeConn([]byte(req))
	if err := srv.ServeConn(fc); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(fc.w.String(), "Injected:") {
		t.Fatalf("CRLF injection leaked: %q", fc.w.String())
	}
}

func TestHEADOmitsBody(t *testing.T) {
	req := "HEAD / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) {
		ctx.WriteString("should-not-appear")
	}}
	fc := newFakeConn([]byte(req))
	if err := srv.ServeConn(fc); err != nil {
		t.Fatal(err)
	}
	resp := fc.w.String()
	if !strings.Contains(resp, "Content-Length: 17\r\n") {
		t.Fatalf("missing CL: %q", resp)
	}
	if strings.Contains(resp, "should-not-appear") {
		t.Fatalf("HEAD leaked body: %q", resp)
	}
}

func TestMethodEqualAndRemoteAddr(t *testing.T) {
	req := "GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
	var methodOK bool
	var remote string
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) {
		methodOK = ctx.MethodEqual("get") && ctx.MethodEqual("GET")
		remote = ctx.RemoteAddr()
		ctx.WriteString("ok")
	}}
	fc := newFakeConn([]byte(req))
	if err := srv.ServeConn(fc); err != nil {
		t.Fatal(err)
	}
	if !methodOK {
		t.Fatal("MethodEqual failed")
	}
	if remote == "" {
		t.Fatal("expected RemoteAddr")
	}
}

func TestExpectContinue(t *testing.T) {
	req := "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 5\r\nExpect: 100-continue\r\nConnection: close\r\n\r\nhello"
	var got string
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) {
		got = string(ctx.Body())
		ctx.WriteString("ok")
	}}
	fc := newFakeConn([]byte(req))
	if err := srv.ServeConn(fc); err != nil {
		t.Fatal(err)
	}
	if got != "hello" {
		t.Fatalf("body %q", got)
	}
	resp := fc.w.String()
	if !strings.HasPrefix(resp, "HTTP/1.1 100 Continue\r\n\r\n") {
		t.Fatalf("missing 100 Continue: %q", resp)
	}
	if !strings.Contains(resp, "HTTP/1.1 200 OK") {
		t.Fatalf("missing final response: %q", resp)
	}
}

func TestContinueTimeoutHappyPath(t *testing.T) {
	req := "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 5\r\nExpect: 100-continue\r\nConnection: close\r\n\r\nhello"
	var got string
	srv := &rawhttp.Server{
		ContinueTimeout: time.Second,
		ReadTimeout:     time.Second,
		WriteTimeout:    time.Second,
		IdleTimeout:     time.Second,
		Handler: func(ctx *rawhttp.Ctx) {
			got = string(ctx.Body())
			ctx.SetBody([]byte("ok"))
		},
	}
	fc := newFakeConn([]byte(req))
	if err := srv.ServeConn(fc); err != nil {
		t.Fatal(err)
	}
	if got != "hello" {
		t.Fatalf("body %q", got)
	}
}

func TestConcurrencyWaitTimeout(t *testing.T) {
	hold := make(chan struct{})
	started := make(chan struct{})
	srv := &rawhttp.Server{
		Concurrency:            1,
		ConcurrencyWaitTimeout: 40 * time.Millisecond,
		ReadTimeout:            -1, WriteTimeout: -1, IdleTimeout: -1,
		Handler: func(ctx *rawhttp.Ctx) {
			select {
			case <-started:
			default:
				close(started)
			}
			<-hold
			ctx.SetBody([]byte("ok"))
		},
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		close(hold)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	c1, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c1.Close()
	_, _ = c1.Write([]byte("GET / HTTP/1.1\r\nHost: localhost\r\n\r\n"))
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("handler did not start")
	}

	c2, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	_ = c2.SetDeadline(time.Now().Add(500 * time.Millisecond))
	_, _ = c2.Write([]byte("GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"))
	buf := make([]byte, 64)
	n, err := c2.Read(buf)
	// Connection should be closed without a response (slot wait timed out).
	if err == nil && n > 0 {
		t.Fatalf("expected closed/empty, got %q err=%v", buf[:n], err)
	}
}

func TestHostRequestURIVisitHeader(t *testing.T) {
	req := "GET /search?q=1 HTTP/1.1\r\nHost: example.com\r\nX-A: 1\r\nX-B: 2\r\nConnection: close\r\n\r\n"
	var host, uri string
	var n int
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) {
		host = string(ctx.Host())
		uri = string(ctx.RequestURI())
		ctx.VisitHeader(func(k, v []byte) { n++ })
	}}
	fc := newFakeConn([]byte(req))
	if err := srv.ServeConn(fc); err != nil {
		t.Fatal(err)
	}
	if host != "example.com" || uri != "/search?q=1" {
		t.Fatalf("host=%q uri=%q", host, uri)
	}
	if n < 3 {
		t.Fatalf("expected >=3 headers, got %d", n)
	}
}

func TestAbsoluteFormRejected(t *testing.T) {
	req := "GET http://evil/ HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) { t.Fatal("handler") }}
	fc := newFakeConn([]byte(req))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "400") {
		t.Fatalf("expected 400, got %q", fc.w.String())
	}
}

func TestRedirectAndConnectionClose(t *testing.T) {
	req := "GET /old HTTP/1.1\r\nHost: localhost\r\n\r\n"
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) {
		_ = ctx.Redirect("/new", 301)
		ctx.SetConnectionClose()
	}}
	fc := newFakeConn([]byte(req))
	if err := srv.ServeConn(fc); err != nil {
		t.Fatal(err)
	}
	resp := fc.w.String()
	if !strings.Contains(resp, "301 Moved Permanently") {
		t.Fatalf("status: %q", resp)
	}
	if !strings.Contains(resp, "Location: /new\r\n") {
		t.Fatalf("location: %q", resp)
	}
	if !strings.Contains(resp, "Connection: close\r\n") {
		t.Fatalf("close: %q", resp)
	}
}

func TestRedirectRejectsOpenRedirect(t *testing.T) {
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) {
		if err := ctx.Redirect("//evil.example/", 302); err != rawhttp.ErrHeaderInvalid {
			t.Fatalf("want ErrHeaderInvalid, got %v", err)
		}
		ctx.SetStatusCode(400)
		ctx.SetBodyString("bad")
	}, ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1}
	fc := newFakeConn([]byte("GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"))
	_ = srv.ServeConn(fc)
	if strings.Contains(fc.w.String(), "Location:") {
		t.Fatalf("must not set Location: %q", fc.w.String())
	}
}

func TestSecureAndDeleteCookie(t *testing.T) {
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) {
		_ = ctx.SecureCookie("sid", "abc")
		_ = ctx.SetCookie(&rawhttp.Cookie{Name: "x", Value: "1", SameSite: rawhttp.SameSiteNoneMode})
		_ = ctx.DeleteCookie("gone")
		ctx.SetBodyString("ok")
	}, ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1}
	fc := newFakeConn([]byte("GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"))
	_ = srv.ServeConn(fc)
	resp := fc.w.String()
	if !strings.Contains(resp, "Set-Cookie: sid=abc; Path=/; HttpOnly; Secure; SameSite=Lax") {
		t.Fatalf("secure cookie: %q", resp)
	}
	if !strings.Contains(resp, "Set-Cookie: x=1; Secure; SameSite=None") {
		t.Fatalf("samesite none: %q", resp)
	}
	if !strings.Contains(resp, "Set-Cookie: gone=; Path=/; Max-Age=0") {
		t.Fatalf("delete cookie: %q", resp)
	}
}

func TestChunkedResponse(t *testing.T) {
	req := "GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) {
		ctx.SetChunked()
		ctx.WriteString("hello")
		ctx.Write([]byte(" world"))
	}}
	fc := newFakeConn([]byte(req))
	if err := srv.ServeConn(fc); err != nil {
		t.Fatal(err)
	}
	resp := fc.w.String()
	if !strings.Contains(resp, "Transfer-Encoding: chunked\r\n") {
		t.Fatalf("missing TE: %q", resp)
	}
	if strings.Contains(resp, "Content-Length:") {
		t.Fatalf("unexpected CL: %q", resp)
	}
	if !strings.Contains(resp, "5\r\nhello\r\n") {
		t.Fatalf("missing chunk1: %q", resp)
	}
	if !strings.Contains(resp, "6\r\n world\r\n") {
		t.Fatalf("missing chunk2: %q", resp)
	}
	if !strings.Contains(resp, "0\r\n\r\n") {
		t.Fatalf("missing end chunk: %q", resp)
	}
}

func TestErrorHelper(t *testing.T) {
	req := "GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) {
		ctx.Error("nope", 404)
	}}
	fc := newFakeConn([]byte(req))
	if err := srv.ServeConn(fc); err != nil {
		t.Fatal(err)
	}
	resp := fc.w.String()
	if !strings.Contains(resp, "404 Not Found") || !strings.HasSuffix(resp, "nope") {
		t.Fatalf("resp %q", resp)
	}
}

func TestTransferEncodingGzipRejected(t *testing.T) {
	req := "POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: gzip\r\nConnection: close\r\n\r\n"
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) { t.Fatal("handler") }}
	fc := newFakeConn([]byte(req))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "400") {
		t.Fatalf("expected 400, got %q", fc.w.String())
	}
}

func TestNoContentOmitsBody(t *testing.T) {
	req := "GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) {
		ctx.SetStatusCode(204)
		ctx.WriteString("should-not-send")
	}}
	fc := newFakeConn([]byte(req))
	if err := srv.ServeConn(fc); err != nil {
		t.Fatal(err)
	}
	resp := fc.w.String()
	if !strings.Contains(resp, "204 No Content") {
		t.Fatalf("status: %q", resp)
	}
	if strings.Contains(resp, "should-not-send") {
		t.Fatalf("body leaked: %q", resp)
	}
}

func TestMaxRequestsPerConn(t *testing.T) {
	req := strings.Repeat("GET / HTTP/1.1\r\nHost: x\r\n\r\n", 5)
	count := 0
	srv := &rawhttp.Server{
		MaxRequestsPerConn: 2,
		Handler: func(ctx *rawhttp.Ctx) {
			count++
			ctx.WriteString("ok")
		},
	}
	fc := newFakeConn([]byte(req))
	_ = srv.ServeConn(fc)
	if count != 2 {
		t.Fatalf("expected 2 requests served, got %d", count)
	}
	if strings.Count(fc.w.String(), "Connection: close") < 1 {
		t.Fatalf("expected close on last response: %q", fc.w.String())
	}
}

func TestDuplicateHostRejected(t *testing.T) {
	req := "GET / HTTP/1.1\r\nHost: a\r\nHost: b\r\nConnection: close\r\n\r\n"
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) { t.Fatal("handler") }}
	fc := newFakeConn([]byte(req))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "400") {
		t.Fatalf("expected 400, got %q", fc.w.String())
	}
}

func TestObsFoldRejected(t *testing.T) {
	req := "GET / HTTP/1.1\r\nHost: localhost\r\nX-A: 1\r\n folded\r\nConnection: close\r\n\r\n"
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) { t.Fatal("handler") }}
	fc := newFakeConn([]byte(req))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "400") {
		t.Fatalf("expected 400, got %q", fc.w.String())
	}
}

func TestContentLengthLeadingZeroRejected(t *testing.T) {
	req := "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 05\r\nConnection: close\r\n\r\nhello"
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) { t.Fatal("handler") }}
	fc := newFakeConn([]byte(req))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "400") {
		t.Fatalf("expected 400, got %q", fc.w.String())
	}
}

func TestHTTP20Rejected(t *testing.T) {
	req := "GET / HTTP/2.0\r\nHost: localhost\r\nConnection: close\r\n\r\n"
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) { t.Fatal("handler") }}
	fc := newFakeConn([]byte(req))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "400") {
		t.Fatalf("expected 400, got %q", fc.w.String())
	}
}

func TestExpectUnsupported417(t *testing.T) {
	req := "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 0\r\nExpect: watermelon\r\nConnection: close\r\n\r\n"
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) { t.Fatal("handler") }}
	fc := newFakeConn([]byte(req))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "417") {
		t.Fatalf("expected 417, got %q", fc.w.String())
	}
}

func TestMaxHeaders(t *testing.T) {
	var b strings.Builder
	b.WriteString("GET / HTTP/1.1\r\nHost: localhost\r\n")
	for i := 0; i < 5; i++ {
		b.WriteString("X-H: v\r\n")
	}
	b.WriteString("Connection: close\r\n\r\n")
	srv := &rawhttp.Server{
		MaxHeaders: 3,
		Handler:    func(ctx *rawhttp.Ctx) { t.Fatal("handler") },
	}
	fc := newFakeConn([]byte(b.String()))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "400") {
		t.Fatalf("expected 400, got %q", fc.w.String())
	}
}

func TestUpgradeRejected(t *testing.T) {
	req := "GET / HTTP/1.1\r\nHost: localhost\r\nUpgrade: websocket\r\nConnection: close\r\n\r\n"
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) { t.Fatal("handler") }}
	fc := newFakeConn([]byte(req))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "400") {
		t.Fatalf("expected 400, got %q", fc.w.String())
	}
}

func TestHostWithSpaceRejected(t *testing.T) {
	req := "GET / HTTP/1.1\r\nHost: bad host\r\nConnection: close\r\n\r\n"
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) { t.Fatal("handler") }}
	fc := newFakeConn([]byte(req))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "400") {
		t.Fatalf("expected 400, got %q", fc.w.String())
	}
}

func TestChunkTrailerLimit(t *testing.T) {
	req := "POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n" +
		"0\r\nX-1: a\r\nX-2: b\r\nX-3: c\r\n\r\n"
	srv := &rawhttp.Server{
		MaxHeaders: 2,
		Handler:    func(ctx *rawhttp.Ctx) { t.Fatal("handler") },
	}
	fc := newFakeConn([]byte(req))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "400") {
		t.Fatalf("expected 400, got %q", fc.w.String())
	}
}

func TestStatsCounters(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) {
		ctx.WriteString("ok")
		ctx.SetConnectionClose()
	}}
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	_, _ = conn.Write([]byte("GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"))
	buf := make([]byte, 256)
	_, _ = conn.Read(buf)
	_ = conn.Close()
	time.Sleep(50 * time.Millisecond)
	if srv.TotalConnections.Load() < 1 || srv.TotalRequests.Load() < 1 {
		t.Fatalf("stats conns=%d reqs=%d", srv.TotalConnections.Load(), srv.TotalRequests.Load())
	}
}

func TestGETWithBodyRejected(t *testing.T) {
	req := "GET / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 5\r\nConnection: close\r\n\r\nhello"
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) { t.Fatal("handler") }}
	fc := newFakeConn([]byte(req))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "400") {
		t.Fatalf("expected 400, got %q", fc.w.String())
	}
}

func TestPathBackslashRejected(t *testing.T) {
	req := "GET /a\\b HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) { t.Fatal("handler") }}
	fc := newFakeConn([]byte(req))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "400") {
		t.Fatalf("expected 400, got %q", fc.w.String())
	}
}

func TestDuplicateTransferEncodingRejected(t *testing.T) {
	req := "POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n0\r\n\r\n"
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) { t.Fatal("handler") }}
	fc := newFakeConn([]byte(req))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "400") {
		t.Fatalf("expected 400, got %q", fc.w.String())
	}
}

func TestConnectionUpgradeTokenRejected(t *testing.T) {
	req := "GET / HTTP/1.1\r\nHost: localhost\r\nConnection: keep-alive, upgrade\r\n\r\n"
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) { t.Fatal("handler") }}
	fc := newFakeConn([]byte(req))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "400") {
		t.Fatalf("expected 400, got %q", fc.w.String())
	}
}

func TestHeaderValueCTLRejected(t *testing.T) {
	req := "GET / HTTP/1.1\r\nHost: localhost\r\nX-Bad: a\x01b\r\nConnection: close\r\n\r\n"
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) { t.Fatal("handler") }}
	fc := newFakeConn([]byte(req))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "400") {
		t.Fatalf("expected 400, got %q", fc.w.String())
	}
}

func TestChunkExtensionTooLongRejected(t *testing.T) {
	ext := strings.Repeat("a", 300)
	req := "POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n" +
		"0;" + ext + "\r\n\r\n"
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) { t.Fatal("handler") }}
	fc := newFakeConn([]byte(req))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "400") {
		t.Fatalf("expected 400, got %q", fc.w.String())
	}
}

func TestNotFoundAndQueryHas(t *testing.T) {
	req := "GET /x?a=1&b= HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
	var hasA, hasB, hasC bool
	var n int
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) {
		qa := ctx.QueryArgs()
		hasA, hasB, hasC = qa.Has("a"), qa.Has("b"), qa.Has("c")
		qa.Visit(func(k, v []byte) { n++ })
		ctx.NotFound()
	}}
	fc := newFakeConn([]byte(req))
	if err := srv.ServeConn(fc); err != nil {
		t.Fatal(err)
	}
	if !hasA || !hasB || hasC || n != 2 {
		t.Fatalf("hasA=%v hasB=%v hasC=%v n=%d", hasA, hasB, hasC, n)
	}
	if !strings.Contains(fc.w.String(), "404") || !strings.Contains(fc.w.String(), "Not Found") {
		t.Fatalf("resp %q", fc.w.String())
	}
}

func TestErrorCallback(t *testing.T) {
	req := "GET / HTTP/1.1\r\nConnection: close\r\n\r\n" // missing Host
	var saw error
	srv := &rawhttp.Server{
		ErrorCallback: func(err error) { saw = err },
		Handler:       func(ctx *rawhttp.Ctx) { t.Fatal("handler") },
	}
	fc := newFakeConn([]byte(req))
	_ = srv.ServeConn(fc)
	if saw == nil {
		t.Fatal("expected ErrorCallback")
	}
}

func TestTRACEAndCONNECTRejected(t *testing.T) {
	for _, method := range []string{"TRACE", "trace", "CONNECT", "connect"} {
		req := method + " / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
		srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) { t.Fatal("handler") }}
		fc := newFakeConn([]byte(req))
		_ = srv.ServeConn(fc)
		if !strings.Contains(fc.w.String(), "400") {
			t.Fatalf("%s: expected 400, got %q", method, fc.w.String())
		}
	}
}

func TestForbiddenTrailerRejected(t *testing.T) {
	req := "POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n" +
		"0\r\nContent-Length: 5\r\n\r\n"
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) { t.Fatal("handler") }}
	fc := newFakeConn([]byte(req))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "400") {
		t.Fatalf("expected 400, got %q", fc.w.String())
	}
}

func TestAsteriskFormOnlyOPTIONS(t *testing.T) {
	req := "GET * HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) { t.Fatal("handler") }}
	fc := newFakeConn([]byte(req))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "400") {
		t.Fatalf("GET *: expected 400, got %q", fc.w.String())
	}

	var path string
	req2 := "OPTIONS * HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
	srv2 := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) {
		path = string(ctx.Path)
		ctx.SetStatusCode(204)
	}}
	fc2 := newFakeConn([]byte(req2))
	if err := srv2.ServeConn(fc2); err != nil {
		t.Fatal(err)
	}
	if path != "*" || !strings.Contains(fc2.w.String(), "204") {
		t.Fatalf("OPTIONS *: path=%q resp=%q", path, fc2.w.String())
	}
}

func TestPathFragmentAndNullPctRejected(t *testing.T) {
	for _, target := range []string{"/a#b", "/a%00b"} {
		req := "GET " + target + " HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
		srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) { t.Fatal("handler") }}
		fc := newFakeConn([]byte(req))
		_ = srv.ServeConn(fc)
		if !strings.Contains(fc.w.String(), "400") {
			t.Fatalf("%s: expected 400, got %q", target, fc.w.String())
		}
	}
}

func TestTEMustBeExactlyChunked(t *testing.T) {
	req := "POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked trailing\r\nConnection: close\r\n\r\n0\r\n\r\n"
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) { t.Fatal("handler") }}
	fc := newFakeConn([]byte(req))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "400") {
		t.Fatalf("expected 400, got %q", fc.w.String())
	}
}

func TestHostTrailingOWSTrimmed(t *testing.T) {
	req := "GET / HTTP/1.1\r\nHost: example.com \t\r\nConnection: close\r\n\r\n"
	var host string
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) {
		host = string(ctx.Host())
		ctx.SetBody([]byte("ok"))
	}}
	fc := newFakeConn([]byte(req))
	if err := srv.ServeConn(fc); err != nil {
		t.Fatal(err)
	}
	if host != "example.com" {
		t.Fatalf("host=%q", host)
	}
}

func TestMethodNotAllowedAndSetHeaderToken(t *testing.T) {
	req := "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) {
		if err := ctx.SetHeader("Bad Name", "x"); err == nil {
			t.Fatal("expected invalid header name")
		}
		_ = ctx.MethodNotAllowed("GET, HEAD")
	}}
	fc := newFakeConn([]byte(req))
	if err := srv.ServeConn(fc); err != nil {
		t.Fatal(err)
	}
	resp := fc.w.String()
	if !strings.Contains(resp, "405") || !strings.Contains(resp, "Allow: GET, HEAD") {
		t.Fatalf("resp=%q", resp)
	}
}

func TestChunkHexTooWideRejected(t *testing.T) {
	req := "POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n" +
		"00000000000000001\r\nX\r\n0\r\n\r\n"
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) { t.Fatal("handler") }}
	fc := newFakeConn([]byte(req))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "400") {
		t.Fatalf("expected 400, got %q", fc.w.String())
	}
}

func TestBareLFRejected(t *testing.T) {
	req := "GET / HTTP/1.1\nHost: localhost\n\n"
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) { t.Fatal("handler") }}
	fc := newFakeConn([]byte(req))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "400") {
		t.Fatalf("expected 400, got %q", fc.w.String())
	}
}

func TestHostUserinfoRejected(t *testing.T) {
	req := "GET / HTTP/1.1\r\nHost: user@evil\r\nConnection: close\r\n\r\n"
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) { t.Fatal("handler") }}
	fc := newFakeConn([]byte(req))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "400") {
		t.Fatalf("expected 400, got %q", fc.w.String())
	}
}

func TestMaxChunksRejected(t *testing.T) {
	body := "1\r\na\r\n1\r\nb\r\n1\r\nc\r\n0\r\n\r\n"
	req := "POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n" + body
	srv := &rawhttp.Server{
		MaxChunks: 2,
		Handler:   func(ctx *rawhttp.Ctx) { t.Fatal("handler") },
	}
	fc := newFakeConn([]byte(req))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "400") {
		t.Fatalf("expected 400, got %q", fc.w.String())
	}
}

func TestHeaderTooLarge431(t *testing.T) {
	req := "GET / HTTP/1.1\r\nHost: localhost\r\nX: " + strings.Repeat("a", 200) + "\r\nConnection: close\r\n\r\n"
	srv := &rawhttp.Server{
		MaxHeaderBytes: 64,
		Handler:        func(ctx *rawhttp.Ctx) { t.Fatal("handler") },
	}
	fc := newFakeConn([]byte(req))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "431") {
		t.Fatalf("expected 431, got %q", fc.w.String())
	}
}

func TestReadBufferSizeRespectsMaxHeaderBytes(t *testing.T) {
	req := "GET / HTTP/1.1\r\nHost: localhost\r\nX: " + strings.Repeat("b", 200) + "\r\nConnection: close\r\n\r\n"
	srv := &rawhttp.Server{
		MaxHeaderBytes: 64,
		ReadBufferSize: 8192,
		Handler:        func(ctx *rawhttp.Ctx) { t.Fatal("handler") },
		ReadTimeout:    -1, WriteTimeout: -1, IdleTimeout: -1,
	}
	fc := newFakeConn([]byte(req))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "431") {
		t.Fatalf("expected 431 with large ReadBufferSize: %q", fc.w.String())
	}
}

func TestIndexedAuthOriginAccept(t *testing.T) {
	var auth, origin, accept string
	var put, del, patch, opts bool
	srv := &rawhttp.Server{
		Handler: func(ctx *rawhttp.Ctx) {
			auth = string(ctx.Authorization())
			origin = string(ctx.Origin())
			accept = string(ctx.Accept())
			put, del, patch, opts = ctx.IsPut(), ctx.IsDelete(), ctx.IsPatch(), ctx.IsOptions()
			ctx.SetBodyString("ok")
		},
		ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
	}
	fc := newFakeConn([]byte(
		"PUT / HTTP/1.1\r\nHost: localhost\r\nAuthorization: Bearer tok\r\nOrigin: https://a.test\r\nAccept: text/plain\r\nContent-Length: 0\r\nConnection: close\r\n\r\n",
	))
	_ = srv.ServeConn(fc)
	if auth != "Bearer tok" || origin != "https://a.test" || accept != "text/plain" {
		t.Fatalf("auth=%q origin=%q accept=%q", auth, origin, accept)
	}
	if !put || del || patch || opts {
		t.Fatalf("methods put=%v del=%v patch=%v opts=%v", put, del, patch, opts)
	}
}

func TestPathHasPrefixAndSetBodyString(t *testing.T) {
	req := "GET /api/v1/x HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
	var ok bool
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) {
		ok = ctx.PathHasPrefix("/api/")
		ctx.SetBodyString("hi")
	}}
	fc := newFakeConn([]byte(req))
	if err := srv.ServeConn(fc); err != nil {
		t.Fatal(err)
	}
	if !ok || !strings.Contains(fc.w.String(), "hi") {
		t.Fatalf("ok=%v resp=%q", ok, fc.w.String())
	}
}

func TestUnauthorized(t *testing.T) {
	req := "GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) { ctx.Unauthorized() }}
	fc := newFakeConn([]byte(req))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "401") {
		t.Fatalf("resp=%q", fc.w.String())
	}
}

func TestDotDotPathRejected(t *testing.T) {
	for _, target := range []string{"/../x", "/a/../b", "/..", "/a/.."} {
		req := "GET " + target + " HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
		srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) { t.Fatal("handler") }}
		fc := newFakeConn([]byte(req))
		_ = srv.ServeConn(fc)
		if !strings.Contains(fc.w.String(), "400") {
			t.Fatalf("%s: expected 400, got %q", target, fc.w.String())
		}
	}
}

func TestTEHeaderRejected(t *testing.T) {
	req := "GET / HTTP/1.1\r\nHost: localhost\r\nTE: trailers\r\nConnection: close\r\n\r\n"
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) { t.Fatal("handler") }}
	fc := newFakeConn([]byte(req))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "400") {
		t.Fatalf("expected 400, got %q", fc.w.String())
	}
}

func TestExpectContinueOnGETRejected(t *testing.T) {
	req := "GET / HTTP/1.1\r\nHost: localhost\r\nExpect: 100-continue\r\nConnection: close\r\n\r\n"
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) { t.Fatal("handler") }}
	fc := newFakeConn([]byte(req))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "400") {
		t.Fatalf("expected 400, got %q", fc.w.String())
	}
}

func TestHostCommaRejected(t *testing.T) {
	req := "GET / HTTP/1.1\r\nHost: a,b\r\nConnection: close\r\n\r\n"
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) { t.Fatal("handler") }}
	fc := newFakeConn([]byte(req))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "400") {
		t.Fatalf("expected 400, got %q", fc.w.String())
	}
}

func TestQueryLenAndHTTP11(t *testing.T) {
	req := "GET /x?a=1&b=2 HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
	var n int
	var v11 bool
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) {
		n = ctx.QueryArgs().Len()
		v11 = ctx.IsHTTP11()
		ctx.SetBodyString("ok")
	}}
	fc := newFakeConn([]byte(req))
	if err := srv.ServeConn(fc); err != nil {
		t.Fatal(err)
	}
	if n != 2 || !v11 {
		t.Fatalf("n=%d v11=%v", n, v11)
	}
}

func TestCloseStopsListener(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) { ctx.SetBodyString("ok") }}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	time.Sleep(20 * time.Millisecond)
	if err := srv.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil && err != rawhttp.ErrServerClosed {
			t.Fatalf("serve: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after Close")
	}
}

func TestProxyConnectionRejected(t *testing.T) {
	req := "GET / HTTP/1.1\r\nHost: localhost\r\nProxy-Connection: keep-alive\r\nConnection: close\r\n\r\n"
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) { t.Fatal("handler") }}
	fc := newFakeConn([]byte(req))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "400") {
		t.Fatalf("expected 400, got %q", fc.w.String())
	}
}

func TestHeaderNameTooLongRejected(t *testing.T) {
	name := strings.Repeat("a", 300)
	req := "GET / HTTP/1.1\r\nHost: localhost\r\n" + name + ": x\r\nConnection: close\r\n\r\n"
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) { t.Fatal("handler") }}
	fc := newFakeConn([]byte(req))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "400") {
		t.Fatalf("expected 400, got %q", fc.w.String())
	}
}

func TestInvalidStatusCodeClamped(t *testing.T) {
	req := "GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) {
		ctx.SetStatusCode(42)
		ctx.SetBodyString("x")
	}}
	fc := newFakeConn([]byte(req))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "500") {
		t.Fatalf("expected 500, got %q", fc.w.String())
	}
}

func TestResetContentOmitsBody(t *testing.T) {
	req := "GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) {
		ctx.SetStatusCode(205)
		ctx.SetBodyString("nope")
	}}
	fc := newFakeConn([]byte(req))
	_ = srv.ServeConn(fc)
	resp := fc.w.String()
	if !strings.Contains(resp, "205") || strings.Contains(resp, "nope") {
		t.Fatalf("resp=%q", resp)
	}
}

func TestSetHeaderCTLRejected(t *testing.T) {
	req := "GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) {
		if err := ctx.SetHeader("X-A", "a\x01b"); err == nil {
			t.Fatal("expected ErrHeaderInvalid")
		}
		ctx.BadRequest()
	}}
	fc := newFakeConn([]byte(req))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "400") {
		t.Fatalf("resp=%q", fc.w.String())
	}
}

func TestRemoteIP(t *testing.T) {
	req := "GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
	var ip string
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) {
		ip = ctx.RemoteIP()
		ctx.SetBodyString("ok")
	}}
	fc := newFakeConn([]byte(req))
	_ = srv.ServeConn(fc)
	if ip == "" {
		t.Fatal("expected RemoteIP")
	}
}
