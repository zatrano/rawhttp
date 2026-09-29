package test_test

import (
	"strings"
	"testing"

	"github.com/zatrano/rawhttp"
)

func TestHealthAndReady(t *testing.T) {
	srv := &rawhttp.Server{Handler: rawhttp.HealthCheck, ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1}
	fc := newFakeConn([]byte("GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "ok") {
		t.Fatalf("health: %q", fc.w.String())
	}

	ready := false
	srv = &rawhttp.Server{Handler: rawhttp.ReadyCheck(func() bool { return ready }), ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1}
	fc = newFakeConn([]byte("GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "503") {
		t.Fatalf("not ready: %q", fc.w.String())
	}
	ready = true
	fc = newFakeConn([]byte("GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "ready") {
		t.Fatalf("ready: %q", fc.w.String())
	}
}

func TestRecoverMiddleware(t *testing.T) {
	var saw any
	h := rawhttp.RecoverMiddleware(func(ctx *rawhttp.Ctx) {
		panic("boom")
	}, func(v any) { saw = v })
	srv := &rawhttp.Server{Handler: h, ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1}
	fc := newFakeConn([]byte("GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"))
	_ = srv.ServeConn(fc)
	if saw != "boom" {
		t.Fatalf("onPanic=%v", saw)
	}
	if !strings.Contains(fc.w.String(), "500") {
		t.Fatalf("want 500: %q", fc.w.String())
	}
}
