package test_test

import (
	"strings"
	"testing"

	"github.com/zatrano/rawhttp"
)

// Upgrade requests are rejected before the handler runs (400) unless
// Server.AllowUpgrade is set. Hijack without Upgrade remains available for
// custom protocols.

func TestWebSocketUpgradeNeverReachesHandler(t *testing.T) {
	req := "" +
		"GET /ws HTTP/1.1\r\n" +
		"Host: localhost\r\n" +
		"Connection: Upgrade\r\n" +
		"Upgrade: websocket\r\n" +
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n" +
		"Sec-WebSocket-Version: 13\r\n" +
		"\r\n"

	called := false
	srv := &rawhttp.Server{
		ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
		KeepHijackedConns: true,
		Handler: func(ctx *rawhttp.Ctx) {
			called = true
			_, _, _ = ctx.Hijack()
		},
	}
	fc := newFakeConn([]byte(req))
	_ = srv.ServeConn(fc)
	resp := fc.w.String()
	if called {
		t.Fatalf("handler must not run for Upgrade handshake; resp=%q", resp)
	}
	if !strings.Contains(resp, "400") {
		t.Fatalf("want 400-class rejection, got %q", resp)
	}
}

func TestUpgradeReject_ConnectionUpgradeToken(t *testing.T) {
	req := "GET / HTTP/1.1\r\nHost: localhost\r\nConnection: keep-alive, upgrade\r\n\r\n"
	called := false
	srv := &rawhttp.Server{
		ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
		Handler: func(ctx *rawhttp.Ctx) { called = true },
	}
	fc := newFakeConn([]byte(req))
	_ = srv.ServeConn(fc)
	if called {
		t.Fatal("handler must not run when Connection contains upgrade")
	}
	if !strings.Contains(fc.w.String(), "400") {
		t.Fatalf("want 400, got %q", fc.w.String())
	}
}

func TestHijackWorksWithoutUpgradeHeader(t *testing.T) {
	// Hijack succeeds on a normal GET (no Upgrade) — custom protocols only.
	req := "GET /custom HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
	hijacked := false
	srv := &rawhttp.Server{
		ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
		KeepHijackedConns: true,
		Handler: func(ctx *rawhttp.Ctx) {
			conn, _, err := ctx.Hijack()
			if err != nil {
				t.Errorf("Hijack: %v", err)
				return
			}
			hijacked = true
			_, _ = conn.Write([]byte("CUSTOM"))
			_ = conn.Close()
		},
	}
	fc := newFakeConn([]byte(req))
	_ = srv.ServeConn(fc)
	if !hijacked {
		t.Fatal("expected Hijack success without Upgrade")
	}
	if !strings.Contains(fc.w.String(), "CUSTOM") {
		t.Fatalf("want custom bytes, got %q", fc.w.String())
	}
}
