package test_test

import (
	"strings"
	"testing"

	"github.com/zatrano/rawhttp"
)

func TestSecureHeadersMiddleware(t *testing.T) {
	h := rawhttp.SecureHeadersMiddleware(func(ctx *rawhttp.Ctx) {
		ctx.SetBody([]byte("ok"))
	}, rawhttp.SecureHeadersOptions{
		ContentSecurityPolicy:   "default-src 'self'",
		PermissionsPolicy:       "geolocation=()",
		CrossOriginOpenerPolicy: "same-origin",
	})
	srv := &rawhttp.Server{Handler: h, ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1}
	fc := newFakeConn([]byte("GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"))
	_ = srv.ServeConn(fc)
	resp := fc.w.String()
	for _, want := range []string{
		"X-Content-Type-Options: nosniff",
		"X-Frame-Options: DENY",
		"Referrer-Policy: no-referrer",
		"Content-Security-Policy: default-src 'self'",
		"Permissions-Policy: geolocation=()",
		"Cross-Origin-Opener-Policy: same-origin",
	} {
		if !strings.Contains(resp, want) {
			t.Fatalf("missing %q in %q", want, resp)
		}
	}
}

func TestHeaderReceivedBodyLimit(t *testing.T) {
	srv := &rawhttp.Server{
		MaxRequestBodySize: 100,
		HeaderReceived:     rawhttp.BodyLimitConfig(3),
		Handler:            func(ctx *rawhttp.Ctx) { t.Fatal("should not run") },
		ReadTimeout:        -1, WriteTimeout: -1, IdleTimeout: -1,
	}
	fc := newFakeConn([]byte(
		"POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 5\r\nConnection: close\r\n\r\nhello",
	))
	err := srv.ServeConn(fc)
	if err != rawhttp.ErrBodyTooLarge {
		t.Fatalf("want ErrBodyTooLarge, got %v", err)
	}
	if !strings.Contains(fc.w.String(), "413") {
		t.Fatalf("want 413: %q", fc.w.String())
	}
}

func TestReferer(t *testing.T) {
	var got string
	srv := &rawhttp.Server{
		Handler: func(ctx *rawhttp.Ctx) {
			got = string(ctx.Referer())
			ctx.SetBody([]byte("ok"))
		},
		ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
	}
	fc := newFakeConn([]byte(
		"GET / HTTP/1.1\r\nHost: localhost\r\nReferer: https://example.com/x\r\nConnection: close\r\n\r\n",
	))
	_ = srv.ServeConn(fc)
	if got != "https://example.com/x" {
		t.Fatalf("Referer=%q", got)
	}
}
