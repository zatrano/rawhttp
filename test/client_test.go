package test_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zatrano/rawhttp"
)

func startTestServer(t *testing.T, h rawhttp.Handler) (addr string, cleanup func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &rawhttp.Server{Handler: h, ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1}
	done := make(chan struct{})
	go func() {
		_ = s.Serve(ln)
		close(done)
	}()
	return ln.Addr().String(), func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
		_ = ln.Close()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	}
}

func TestClientGET(t *testing.T) {
	addr, stop := startTestServer(t, func(ctx *rawhttp.Ctx) {
		ctx.SetBody([]byte("hello-client"))
	})
	defer stop()

	c := &rawhttp.Client{ReadTimeout: 2 * time.Second, WriteTimeout: 2 * time.Second, DialTimeout: time.Second}
	resp := rawhttp.AcquireResponse()
	defer rawhttp.ReleaseResponse(resp)

	if err := c.Get("http://"+addr+"/", resp); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if string(resp.Body()) != "hello-client" {
		t.Fatalf("body=%q", resp.Body())
	}
}

func TestClientPOST(t *testing.T) {
	addr, stop := startTestServer(t, func(ctx *rawhttp.Ctx) {
		ctx.SetBody(ctx.Body())
	})
	defer stop()

	c := &rawhttp.Client{}
	resp := rawhttp.AcquireResponse()
	defer rawhttp.ReleaseResponse(resp)
	if err := c.Post("http://"+addr+"/echo", "text/plain", []byte("ping"), resp); err != nil {
		t.Fatal(err)
	}
	if string(resp.Body()) != "ping" {
		t.Fatalf("body=%q", resp.Body())
	}
}

func TestClientPutPatchDelete(t *testing.T) {
	var method string
	var body string
	addr, stop := startTestServer(t, func(ctx *rawhttp.Ctx) {
		method = string(ctx.Method)
		body = string(ctx.Body())
		ctx.SetBody([]byte("ok"))
	})
	defer stop()
	c := &rawhttp.Client{}
	resp := rawhttp.AcquireResponse()
	defer rawhttp.ReleaseResponse(resp)

	if err := c.Put("http://"+addr+"/", "text/plain", []byte("put"), resp); err != nil {
		t.Fatal(err)
	}
	if method != "PUT" || body != "put" {
		t.Fatalf("put method=%q body=%q", method, body)
	}
	resp.Reset()
	if err := c.Patch("http://"+addr+"/", "text/plain", []byte("patch"), resp); err != nil {
		t.Fatal(err)
	}
	if method != "PATCH" || body != "patch" {
		t.Fatalf("patch method=%q body=%q", method, body)
	}
	resp.Reset()
	if err := c.Delete("http://"+addr+"/", resp); err != nil {
		t.Fatal(err)
	}
	if method != "DELETE" {
		t.Fatalf("delete method=%q", method)
	}
}

func TestClientKeepAliveReuse(t *testing.T) {
	var accepts atomic.Int32
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	s := &rawhttp.Server{
		Handler: func(ctx *rawhttp.Ctx) {
			ctx.SetBody([]byte("ok"))
		},
		ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			accepts.Add(1)
			go func(c net.Conn) {
				_ = s.ServeConn(c)
				_ = c.Close()
			}(conn)
		}
	}()

	c := &rawhttp.Client{MaxIdleConnDuration: time.Minute}
	url := "http://" + ln.Addr().String() + "/"
	for i := 0; i < 5; i++ {
		resp := rawhttp.AcquireResponse()
		if err := c.Get(url, resp); err != nil {
			t.Fatal(err)
		}
		if string(resp.Body()) != "ok" {
			t.Fatalf("body=%q", resp.Body())
		}
		rawhttp.ReleaseResponse(resp)
	}
	c.CloseIdleConnections()
	if n := accepts.Load(); n != 1 {
		t.Fatalf("want 1 accept for keep-alive reuse, got %d", n)
	}
}

func TestClientRedirect(t *testing.T) {
	addr, stop := startTestServer(t, func(ctx *rawhttp.Ctx) {
		if ctx.PathEqual("/from") {
			_ = ctx.Redirect("/to", 302)
			return
		}
		ctx.SetBody([]byte("landed"))
	})
	defer stop()

	c := &rawhttp.Client{MaxRedirects: 3}
	resp := rawhttp.AcquireResponse()
	defer rawhttp.ReleaseResponse(resp)
	if err := c.Get("http://"+addr+"/from", resp); err != nil {
		t.Fatal(err)
	}
	if string(resp.Body()) != "landed" {
		t.Fatalf("body=%q status=%d", resp.Body(), resp.StatusCode)
	}
}

func TestClientRedirectStripsAuth(t *testing.T) {
	var sawAuth atomic.Bool
	target, stopT := startTestServer(t, func(ctx *rawhttp.Ctx) {
		if len(ctx.Header("Authorization")) > 0 {
			sawAuth.Store(true)
		}
		ctx.SetBody([]byte("ok"))
	})
	defer stopT()

	origin, stopO := startTestServer(t, func(ctx *rawhttp.Ctx) {
		_ = ctx.Redirect("http://"+target+"/", 302)
	})
	defer stopO()

	c := &rawhttp.Client{MaxRedirects: 2}
	req := rawhttp.AcquireRequest()
	defer rawhttp.ReleaseRequest(req)
	req.Method = "GET"
	req.RequestURI = "http://" + origin + "/"
	_ = req.SetHeader("Authorization", "Bearer secret")
	resp := rawhttp.AcquireResponse()
	defer rawhttp.ReleaseResponse(resp)
	if err := c.Do(req, resp); err != nil {
		t.Fatal(err)
	}
	if sawAuth.Load() {
		t.Fatal("Authorization must not follow cross-host redirect")
	}
	if string(resp.Body()) != "ok" {
		t.Fatalf("body=%q", resp.Body())
	}
}

func TestClientCheckRedirect(t *testing.T) {
	addr, stop := startTestServer(t, func(ctx *rawhttp.Ctx) {
		if ctx.PathEqual("/from") {
			_ = ctx.Redirect("/to", 302)
			return
		}
		ctx.SetBody([]byte("landed"))
	})
	defer stop()

	c := &rawhttp.Client{
		MaxRedirects: 3,
		CheckRedirect: func(req *rawhttp.Request, via []*rawhttp.Request) error {
			return rawhttp.ErrRedirect
		},
	}
	resp := rawhttp.AcquireResponse()
	defer rawhttp.ReleaseResponse(resp)
	err := c.Get("http://"+addr+"/from", resp)
	if err != rawhttp.ErrRedirect {
		t.Fatalf("want ErrRedirect, got %v", err)
	}
}

func TestClientMaxResponseHeaderBytes(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 4096)
		_, _ = conn.Read(buf)
		pad := strings.Repeat("x", 200)
		_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nX-Pad: "+pad+"\r\nContent-Length: 2\r\nConnection: close\r\n\r\nok")
	}()

	hc := &rawhttp.HostClient{Addr: ln.Addr().String(), MaxResponseHeaderBytes: 64}
	defer hc.CloseIdleConnections()
	req := rawhttp.AcquireRequest()
	defer rawhttp.ReleaseRequest(req)
	req.RequestURI = "/"
	resp := rawhttp.AcquireResponse()
	defer rawhttp.ReleaseResponse(resp)
	if err := hc.Do(req, resp); err != rawhttp.ErrHeaderTooLarge {
		t.Fatalf("want ErrHeaderTooLarge, got %v", err)
	}
}

func TestClientMaxHeaders(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 4096)
		_, _ = conn.Read(buf)
		var b strings.Builder
		b.WriteString("HTTP/1.1 200 OK\r\n")
		for i := 0; i < 6; i++ {
			b.WriteString("X-H" + string(rune('a'+i)) + ": 1\r\n")
		}
		b.WriteString("Content-Length: 2\r\nConnection: close\r\n\r\nok")
		_, _ = io.WriteString(conn, b.String())
	}()

	hc := &rawhttp.HostClient{Addr: ln.Addr().String(), MaxHeaders: 4}
	defer hc.CloseIdleConnections()
	req := rawhttp.AcquireRequest()
	defer rawhttp.ReleaseRequest(req)
	req.RequestURI = "/"
	resp := rawhttp.AcquireResponse()
	defer rawhttp.ReleaseResponse(resp)
	if err := hc.Do(req, resp); err != rawhttp.ErrBadRequest {
		t.Fatalf("want ErrBadRequest for too many headers, got %v", err)
	}
}

func TestClientSetBodyStream(t *testing.T) {
	var got string
	addr, stop := startTestServer(t, func(ctx *rawhttp.Ctx) {
		got = string(ctx.Body())
		ctx.SetBody([]byte("ok"))
	})
	defer stop()

	hc := &rawhttp.HostClient{Addr: addr}
	defer hc.CloseIdleConnections()
	req := rawhttp.AcquireRequest()
	defer rawhttp.ReleaseRequest(req)
	req.Method = "POST"
	req.RequestURI = "/"
	payload := []byte("stream-body")
	req.SetBodyStream(bytes.NewReader(payload), len(payload))
	resp := rawhttp.AcquireResponse()
	defer rawhttp.ReleaseResponse(resp)
	if err := hc.Do(req, resp); err != nil {
		t.Fatal(err)
	}
	if got != "stream-body" {
		t.Fatalf("body=%q", got)
	}
}

func TestClientSetBasicAuth(t *testing.T) {
	var auth string
	addr, stop := startTestServer(t, func(ctx *rawhttp.Ctx) {
		auth = string(ctx.Header("Authorization"))
		ctx.SetBody([]byte("ok"))
	})
	defer stop()
	c := &rawhttp.Client{}
	req := rawhttp.AcquireRequest()
	defer rawhttp.ReleaseRequest(req)
	req.RequestURI = "http://" + addr + "/"
	if err := req.SetBasicAuth("u", "p"); err != nil {
		t.Fatal(err)
	}
	resp := rawhttp.AcquireResponse()
	defer rawhttp.ReleaseResponse(resp)
	if err := c.Do(req, resp); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(auth, "Basic ") {
		t.Fatalf("auth=%q", auth)
	}
}

func TestRequestDelHeader(t *testing.T) {
	req := rawhttp.AcquireRequest()
	defer rawhttp.ReleaseRequest(req)
	_ = req.SetHeader("Authorization", "Bearer x")
	_ = req.SetHeader("X-Keep", "1")
	_ = req.SetHeader("Cookie", "a=b")
	req.DelHeader("Authorization")
	req.DelHeader("Cookie")
	// Round-trip via a sink server that echoes whether headers survived.
	var gotAuth, gotCookie, gotKeep bool
	addr, stop := startTestServer(t, func(ctx *rawhttp.Ctx) {
		gotAuth = len(ctx.Header("Authorization")) > 0
		gotCookie = len(ctx.Header("Cookie")) > 0
		gotKeep = string(ctx.Header("X-Keep")) == "1"
		ctx.SetBody([]byte("ok"))
	})
	defer stop()
	req.RequestURI = "http://" + addr + "/"
	resp := rawhttp.AcquireResponse()
	defer rawhttp.ReleaseResponse(resp)
	c := &rawhttp.Client{}
	if err := c.Do(req, resp); err != nil {
		t.Fatal(err)
	}
	if gotAuth || gotCookie {
		t.Fatalf("auth=%v cookie=%v", gotAuth, gotCookie)
	}
	if !gotKeep {
		t.Fatal("X-Keep should remain")
	}
}

func TestClientMaxResponseBody(t *testing.T) {
	addr, stop := startTestServer(t, func(ctx *rawhttp.Ctx) {
		ctx.SetBody([]byte("0123456789"))
	})
	defer stop()

	c := &rawhttp.Client{MaxResponseBodySize: 4}
	resp := rawhttp.AcquireResponse()
	defer rawhttp.ReleaseResponse(resp)
	err := c.Get("http://"+addr+"/", resp)
	if err != rawhttp.ErrBodyTooLarge {
		t.Fatalf("want ErrBodyTooLarge, got %v", err)
	}
}

func TestClientDoTimeout(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 4096)
		_, _ = conn.Read(buf)
		time.Sleep(500 * time.Millisecond)
		_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nok")
	}()

	c := &rawhttp.Client{}
	req := rawhttp.AcquireRequest()
	defer rawhttp.ReleaseRequest(req)
	req.RequestURI = "http://" + ln.Addr().String() + "/"
	resp := rawhttp.AcquireResponse()
	defer rawhttp.ReleaseResponse(resp)
	err = c.DoTimeout(req, resp, 50*time.Millisecond)
	if err != rawhttp.ErrTimeout {
		t.Fatalf("want ErrTimeout, got %v", err)
	}
}

func TestClientHeaderAndChunked(t *testing.T) {
	addr, stop := startTestServer(t, func(ctx *rawhttp.Ctx) {
		_ = ctx.SetHeader("X-Echo", string(ctx.Header("X-Req")))
		ctx.SetChunked()
		ctx.Write([]byte("ab"))
		ctx.Write([]byte("cd"))
	})
	defer stop()

	c := &rawhttp.Client{}
	req := rawhttp.AcquireRequest()
	defer rawhttp.ReleaseRequest(req)
	req.Method = "GET"
	req.RequestURI = "http://" + addr + "/"
	_ = req.SetHeader("X-Req", "yes")
	resp := rawhttp.AcquireResponse()
	defer rawhttp.ReleaseResponse(resp)
	if err := c.Do(req, resp); err != nil {
		t.Fatal(err)
	}
	if string(resp.Body()) != "abcd" {
		t.Fatalf("body=%q", resp.Body())
	}
	if string(resp.Header("X-Echo")) != "yes" {
		t.Fatalf("header=%q", resp.Header("X-Echo"))
	}
}

func TestClientTLS(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	certPEM, keyPEM := mustTestCert(t)
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	tlsLn := tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{cert}})
	s := &rawhttp.Server{
		Handler:     func(ctx *rawhttp.Ctx) { ctx.SetBody([]byte("secure")) },
		ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
	}
	go func() { _ = s.Serve(tlsLn) }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
		_ = tlsLn.Close()
	}()

	c := &rawhttp.Client{
		TLSConfig: &tls.Config{InsecureSkipVerify: true},
	}
	resp := rawhttp.AcquireResponse()
	defer rawhttp.ReleaseResponse(resp)
	if err := c.Get("https://"+ln.Addr().String()+"/", resp); err != nil {
		t.Fatal(err)
	}
	if string(resp.Body()) != "secure" {
		t.Fatalf("body=%q", resp.Body())
	}
}

func TestHostClientConcurrent(t *testing.T) {
	addr, stop := startTestServer(t, func(ctx *rawhttp.Ctx) {
		ctx.SetBody([]byte("x"))
	})
	defer stop()

	hc := &rawhttp.HostClient{Addr: addr, MaxConns: 8}
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := rawhttp.AcquireRequest()
			defer rawhttp.ReleaseRequest(req)
			req.RequestURI = "/"
			resp := rawhttp.AcquireResponse()
			defer rawhttp.ReleaseResponse(resp)
			if err := hc.Do(req, resp); err != nil {
				t.Error(err)
				return
			}
			if string(resp.Body()) != "x" {
				t.Errorf("body=%q", resp.Body())
			}
		}()
	}
	wg.Wait()
}

func TestClientInvalidHeader(t *testing.T) {
	req := rawhttp.AcquireRequest()
	defer rawhttp.ReleaseRequest(req)
	if err := req.SetHeader("Bad\nName", "x"); err != rawhttp.ErrHeaderInvalid {
		t.Fatalf("got %v", err)
	}
	if err := req.SetHeader("X-Ok", "a\rb"); err != rawhttp.ErrHeaderInvalid {
		t.Fatalf("got %v", err)
	}
}

func TestClientAbsoluteURLRequired(t *testing.T) {
	c := &rawhttp.Client{}
	req := rawhttp.AcquireRequest()
	defer rawhttp.ReleaseRequest(req)
	req.RequestURI = "/relative"
	resp := rawhttp.AcquireResponse()
	defer rawhttp.ReleaseResponse(resp)
	err := c.Do(req, resp)
	if err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("got %v", err)
	}
}

func TestClientRejectsCLAndTE(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 4096)
		_, _ = conn.Read(buf)
		_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\nContent-Length: 1\r\nConnection: close\r\n\r\n0\r\n\r\n")
	}()

	hc := &rawhttp.HostClient{Addr: ln.Addr().String()}
	defer hc.CloseIdleConnections()
	req := rawhttp.AcquireRequest()
	defer rawhttp.ReleaseRequest(req)
	req.RequestURI = "/"
	resp := rawhttp.AcquireResponse()
	defer rawhttp.ReleaseResponse(resp)
	if err := hc.Do(req, resp); err == nil {
		t.Fatal("expected CL+TE rejection")
	}
}

func TestClientRejectsForbiddenTrailer(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 4096)
		_, _ = conn.Read(buf)
		_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n5\r\nhello\r\n0\r\nContent-Length: 5\r\n\r\n")
	}()

	hc := &rawhttp.HostClient{Addr: ln.Addr().String()}
	defer hc.CloseIdleConnections()
	req := rawhttp.AcquireRequest()
	defer rawhttp.ReleaseRequest(req)
	req.RequestURI = "/"
	resp := rawhttp.AcquireResponse()
	defer rawhttp.ReleaseResponse(resp)
	if err := hc.Do(req, resp); err == nil {
		t.Fatal("expected forbidden trailer rejection")
	}
}

func TestClientHEADIgnoresDeclaredBody(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 4096)
		// Two pipelined-style responses on one connection: HEAD then GET.
		_, _ = conn.Read(buf)
		_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Length: 5\r\n\r\n")
		_, _ = conn.Read(buf)
		_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nok")
	}()

	hc := &rawhttp.HostClient{Addr: ln.Addr().String()}
	defer hc.CloseIdleConnections()

	req := rawhttp.AcquireRequest()
	defer rawhttp.ReleaseRequest(req)
	req.Method = "HEAD"
	req.RequestURI = "/"
	resp := rawhttp.AcquireResponse()
	defer rawhttp.ReleaseResponse(resp)
	if err := hc.Do(req, resp); err != nil {
		t.Fatalf("HEAD: %v", err)
	}
	if len(resp.Body()) != 0 {
		t.Fatalf("HEAD body=%q", resp.Body())
	}

	req.Method = "GET"
	resp.Reset()
	if err := hc.Do(req, resp); err != nil {
		t.Fatalf("GET after HEAD: %v", err)
	}
	if string(resp.Body()) != "ok" {
		t.Fatalf("GET body=%q", resp.Body())
	}
}

func TestClientShortHeaderNameNoPanic(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 4096)
		_, _ = conn.Read(buf)
		_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nC: x\r\nT: y\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
	}()

	hc := &rawhttp.HostClient{Addr: ln.Addr().String()}
	defer hc.CloseIdleConnections()
	req := rawhttp.AcquireRequest()
	defer rawhttp.ReleaseRequest(req)
	req.RequestURI = "/"
	resp := rawhttp.AcquireResponse()
	defer rawhttp.ReleaseResponse(resp)
	if err := hc.Do(req, resp); err != nil {
		t.Fatal(err)
	}
}

func TestClientRejectsRequestInjection(t *testing.T) {
	hc := &rawhttp.HostClient{Addr: "127.0.0.1:9"}
	req := rawhttp.AcquireRequest()
	defer rawhttp.ReleaseRequest(req)
	resp := rawhttp.AcquireResponse()
	defer rawhttp.ReleaseResponse(resp)

	req.Method = "GET /x HTTP/1.1\r\nX: y"
	req.RequestURI = "/"
	if err := hc.Do(req, resp); err == nil {
		t.Fatal("expected method CRLF rejection")
	}

	req.Reset()
	req.RequestURI = "/\r\nHost: evil"
	if err := hc.Do(req, resp); err == nil {
		t.Fatal("expected URI CRLF rejection")
	}

	req.Reset()
	req.RequestURI = "/"
	req.Host = "h\r\nX: injected"
	if err := hc.Do(req, resp); err == nil {
		t.Fatal("expected Host CRLF rejection")
	}
}

func TestClientRejectsTooMany1xx(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 4096)
		_, _ = conn.Read(buf)
		var b strings.Builder
		for i := 0; i < 6; i++ {
			b.WriteString("HTTP/1.1 100 Continue\r\n\r\n")
		}
		b.WriteString("HTTP/1.1 200 OK\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
		_, _ = io.WriteString(conn, b.String())
	}()

	hc := &rawhttp.HostClient{Addr: ln.Addr().String()}
	defer hc.CloseIdleConnections()
	req := rawhttp.AcquireRequest()
	defer rawhttp.ReleaseRequest(req)
	req.RequestURI = "/"
	resp := rawhttp.AcquireResponse()
	defer rawhttp.ReleaseResponse(resp)
	if err := hc.Do(req, resp); err == nil {
		t.Fatal("expected too many 1xx rejection")
	}
}

func TestHostClientMaxConnWaitTimeout(t *testing.T) {
	gate := make(chan struct{})
	addr, stop := startTestServer(t, func(ctx *rawhttp.Ctx) {
		<-gate
		ctx.SetBody([]byte("ok"))
	})
	defer stop()

	hc := &rawhttp.HostClient{
		Addr:               addr,
		MaxConns:           1,
		MaxConnWaitTimeout: 30 * time.Millisecond,
	}
	defer hc.CloseIdleConnections()

	started := make(chan struct{})
	errCh := make(chan error, 1)
	go func() {
		req := rawhttp.AcquireRequest()
		defer rawhttp.ReleaseRequest(req)
		req.RequestURI = "/"
		resp := rawhttp.AcquireResponse()
		defer rawhttp.ReleaseResponse(resp)
		close(started)
		errCh <- hc.Do(req, resp)
	}()
	<-started
	time.Sleep(10 * time.Millisecond)

	req := rawhttp.AcquireRequest()
	defer rawhttp.ReleaseRequest(req)
	req.RequestURI = "/"
	resp := rawhttp.AcquireResponse()
	defer rawhttp.ReleaseResponse(resp)
	err := hc.Do(req, resp)
	if err != rawhttp.ErrNoFreeConns {
		t.Fatalf("want ErrNoFreeConns, got %v", err)
	}
	gate <- struct{}{}
	if err := <-errCh; err != nil {
		t.Fatalf("first request: %v", err)
	}
}

func TestClientPostFormAndGetTimeout(t *testing.T) {
	var gotCT, gotBody string
	addr, cleanup := startTestServer(t, func(ctx *rawhttp.Ctx) {
		gotCT = string(ctx.RequestContentType())
		gotBody = string(ctx.PostArgs().Get("a"))
		ctx.SetBodyString("ok")
	})
	defer cleanup()

	c := &rawhttp.Client{}
	resp := rawhttp.AcquireResponse()
	defer rawhttp.ReleaseResponse(resp)
	form := url.Values{"a": {"1"}, "b": {"two"}}
	if err := c.PostForm("http://"+addr+"/", form, resp); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || string(resp.Body()) != "ok" {
		t.Fatalf("status=%d body=%q", resp.StatusCode, resp.Body())
	}
	if !strings.Contains(gotCT, "application/x-www-form-urlencoded") || gotBody != "1" {
		t.Fatalf("ct=%q body field=%q", gotCT, gotBody)
	}

	resp.Reset()
	if err := c.GetTimeout("http://"+addr+"/", resp, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("GetTimeout status=%d", resp.StatusCode)
	}
}
