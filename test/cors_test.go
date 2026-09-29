package test_test

import (
	"strings"
	"testing"

	"github.com/zatrano/rawhttp"
)

func TestCORSMiddleware(t *testing.T) {
	h := rawhttp.CORSMiddleware(func(ctx *rawhttp.Ctx) {
		ctx.SetBody([]byte("ok"))
	}, rawhttp.CORSOptions{AllowOrigin: "https://example.com", MaxAge: 600})

	srv := &rawhttp.Server{Handler: h, ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1}

	fc := newFakeConn([]byte("OPTIONS / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"))
	_ = srv.ServeConn(fc)
	resp := fc.w.String()
	if !strings.Contains(resp, "204") {
		t.Fatalf("want 204 preflight: %q", resp)
	}
	if !strings.Contains(resp, "Access-Control-Allow-Origin: https://example.com") {
		t.Fatalf("missing ACAO: %q", resp)
	}
	if !strings.Contains(resp, "Access-Control-Max-Age: 600") {
		t.Fatalf("missing Max-Age: %q", resp)
	}

	fc = newFakeConn([]byte("GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"))
	_ = srv.ServeConn(fc)
	resp = fc.w.String()
	if !strings.Contains(resp, "ok") {
		t.Fatalf("want body: %q", resp)
	}
	if !strings.Contains(resp, "Access-Control-Allow-Origin: https://example.com") {
		t.Fatalf("missing ACAO on GET: %q", resp)
	}
}

func TestCORSAllowOriginsReflect(t *testing.T) {
	h := rawhttp.CORSMiddleware(func(ctx *rawhttp.Ctx) {
		ctx.SetBody([]byte("ok"))
	}, rawhttp.CORSOptions{
		AllowOrigins:     []string{"https://app.example.com"},
		AllowCredentials: true,
	})
	srv := &rawhttp.Server{Handler: h, ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1}

	req := "GET / HTTP/1.1\r\nHost: localhost\r\nOrigin: https://app.example.com\r\nConnection: close\r\n\r\n"
	fc := newFakeConn([]byte(req))
	_ = srv.ServeConn(fc)
	resp := fc.w.String()
	if !strings.Contains(resp, "Access-Control-Allow-Origin: https://app.example.com") {
		t.Fatalf("want reflected origin: %q", resp)
	}
	if !strings.Contains(resp, "Vary: Origin") {
		t.Fatalf("want Vary: Origin: %q", resp)
	}
	if !strings.Contains(resp, "Access-Control-Allow-Credentials: true") {
		t.Fatalf("want credentials: %q", resp)
	}

	fc = newFakeConn([]byte("GET / HTTP/1.1\r\nHost: localhost\r\nOrigin: https://evil.test\r\nConnection: close\r\n\r\n"))
	_ = srv.ServeConn(fc)
	if strings.Contains(fc.w.String(), "Access-Control-Allow-Origin:") {
		t.Fatalf("disallowed origin must not get ACAO: %q", fc.w.String())
	}
}

func TestCORSCredentialsNotWithStar(t *testing.T) {
	h := rawhttp.CORSMiddleware(func(ctx *rawhttp.Ctx) {
		ctx.SetBody([]byte("ok"))
	}, rawhttp.CORSOptions{AllowOrigin: "*", AllowCredentials: true})
	srv := &rawhttp.Server{Handler: h, ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1}
	fc := newFakeConn([]byte("GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"))
	_ = srv.ServeConn(fc)
	resp := fc.w.String()
	if !strings.Contains(resp, "Access-Control-Allow-Origin: *") {
		t.Fatalf("want *: %q", resp)
	}
	if strings.Contains(resp, "Access-Control-Allow-Credentials") {
		t.Fatalf("credentials must not pair with *: %q", resp)
	}
}

func TestServerName(t *testing.T) {
	srv := &rawhttp.Server{
		Name:        "rawhttp",
		Handler:     func(ctx *rawhttp.Ctx) { ctx.SetBody([]byte("ok")) },
		ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
	}
	fc := newFakeConn([]byte("GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "Server: rawhttp\r\n") {
		t.Fatalf("want Server header: %q", fc.w.String())
	}
}
