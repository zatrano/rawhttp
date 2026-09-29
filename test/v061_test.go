package test_test

import (
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/zatrano/rawhttp"
)

func TestArgsMutable(t *testing.T) {
	var a rawhttp.Args
	a.Set("q", "hello")
	a.Add("q", "world")
	a.Set("page", "1")
	if got := a.GetString("page"); got != "1" {
		t.Fatalf("page=%q", got)
	}
	if a.Len() < 2 {
		t.Fatalf("len=%d", a.Len())
	}
	a.Del("q")
	if a.Has("q") {
		t.Fatal("q should be gone")
	}
	a.SetUint("n", 42)
	if a.GetString("n") != "42" {
		t.Fatalf("n=%q qs=%q", a.GetString("n"), a.String())
	}
}

func TestURIParseAndCtx(t *testing.T) {
	var u rawhttp.URI
	u.Parse("https://user:pass@example.com:8443/a/b?x=1#frag")
	if string(u.Scheme()) != "https" || string(u.Host()) != "example.com:8443" {
		t.Fatalf("scheme/host=%q %q", u.Scheme(), u.Host())
	}
	if string(u.Path()) != "/a/b" || string(u.QueryString()) != "x=1" {
		t.Fatalf("path/query=%q %q", u.Path(), u.QueryString())
	}
	if string(u.Username()) != "user" || string(u.Hash()) != "frag" {
		t.Fatalf("user/hash=%q %q", u.Username(), u.Hash())
	}

	srv := &rawhttp.Server{
		Handler: func(ctx *rawhttp.Ctx) {
			uri := ctx.URI()
			ctx.SetBodyString(string(uri.Path()) + "|" + string(uri.QueryString()))
		},
		ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
	}
	fc := newFakeConn([]byte("GET /p?q=1 HTTP/1.1\r\nHost: h\r\nConnection: close\r\n\r\n"))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "/p|q=1") {
		t.Fatalf("resp=%q", fc.w.String())
	}
}

func TestStreamResponseBody(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		srv := &rawhttp.Server{
			Handler: func(ctx *rawhttp.Ctx) {
				ctx.SetBodyString("streamed-client-body")
			},
			ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
		}
		c, err := ln.Accept()
		if err != nil {
			return
		}
		_ = srv.ServeConn(c)
		_ = c.Close()
	}()

	hc := &rawhttp.HostClient{
		Addr:               ln.Addr().String(),
		StreamResponseBody: true,
		MaxConns:           1,
		DialTimeout:        time.Second,
	}
	defer hc.CloseIdleConnections()
	req := rawhttp.AcquireRequest()
	resp := rawhttp.AcquireResponse()
	defer rawhttp.ReleaseRequest(req)
	defer rawhttp.ReleaseResponse(resp)
	req.RequestURI = "/"
	if err := hc.Do(req, resp); err != nil {
		t.Fatal(err)
	}
	if !resp.IsBodyStream() {
		t.Fatal("expected body stream")
	}
	b, err := io.ReadAll(resp.BodyStream())
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "streamed-client-body" {
		t.Fatalf("body=%q", b)
	}
	if err := resp.CloseBodyStream(); err != nil {
		t.Fatal(err)
	}
}

func TestRequestSetTimeout(t *testing.T) {
	req := rawhttp.AcquireRequest()
	defer rawhttp.ReleaseRequest(req)
	req.SetTimeout(50 * time.Millisecond)
	if req.GetTimeout() != 50*time.Millisecond {
		t.Fatal(req.GetTimeout())
	}
}

func TestDisablePathNormalizing(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var saw string
	go func() {
		srv := &rawhttp.Server{
			DisablePathNormalizing: true,
			Handler: func(ctx *rawhttp.Ctx) {
				saw = string(ctx.Path)
				ctx.SetBodyString("ok")
			},
			ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
		}
		c, err := ln.Accept()
		if err != nil {
			return
		}
		_ = srv.ServeConn(c)
		_ = c.Close()
	}()

	cl := &rawhttp.Client{
		DisablePathNormalizing: true,
		DialTimeout:            time.Second,
	}
	req := rawhttp.AcquireRequest()
	resp := rawhttp.AcquireResponse()
	defer rawhttp.ReleaseRequest(req)
	defer rawhttp.ReleaseResponse(resp)
	req.RequestURI = "http://" + ln.Addr().String() + "/foo/../bar"
	if err := cl.Do(req, resp); err != nil {
		t.Fatal(err)
	}
	if saw != "/foo/../bar" {
		t.Fatalf("path=%q want raw /foo/../bar", saw)
	}
}

func TestReduceMemoryUsage(t *testing.T) {
	srv := &rawhttp.Server{
		ReduceMemoryUsage: true,
		Handler: func(ctx *rawhttp.Ctx) {
			ctx.SetBody(ctx.Body())
		},
		ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
	}
	body := strings.Repeat("x", 8192)
	req := "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 8192\r\nConnection: close\r\n\r\n" + body
	fc := newFakeConn([]byte(req))
	if err := srv.ServeConn(fc); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fc.w.String(), body[:16]) {
		t.Fatalf("missing body echo")
	}
}

func TestErrorHandler(t *testing.T) {
	var saw error
	srv := &rawhttp.Server{
		ErrorHandler: func(ctx *rawhttp.Ctx, err error) { saw = err },
		Handler:      func(ctx *rawhttp.Ctx) {},
		ReadTimeout:  -1, WriteTimeout: -1, IdleTimeout: -1,
	}
	fc := newFakeConn([]byte("BAD\r\n\r\n"))
	_ = srv.ServeConn(fc)
	if saw == nil {
		t.Fatal("expected ErrorHandler call")
	}
}

func TestHijackSetNoResponse(t *testing.T) {
	srv := &rawhttp.Server{
		Handler: func(ctx *rawhttp.Ctx) {
			ctx.HijackSetNoResponse(true)
			conn, _, err := ctx.Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_, _ = conn.Write([]byte("hijacked"))
			_ = conn.Close()
		},
		ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
	}
	fc := newFakeConn([]byte("GET / HTTP/1.1\r\nHost: localhost\r\n\r\n"))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "hijacked") {
		t.Fatalf("resp=%q", fc.w.String())
	}
}
