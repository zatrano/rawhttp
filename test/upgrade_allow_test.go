package test_test

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zatrano/rawhttp"
)

const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

func wsAccept(key string) string {
	h := sha1.Sum([]byte(key + wsGUID))
	return base64.StdEncoding.EncodeToString(h[:])
}

func wsClientKey() (key string, raw [16]byte) {
	for i := 0; i < 16; i++ {
		raw[i] = byte(i + 1)
	}
	return base64.StdEncoding.EncodeToString(raw[:]), raw
}

func wsHandshake(path, key string) string {
	return "GET " + path + " HTTP/1.1\r\n" +
		"Host: localhost\r\n" +
		"Connection: keep-alive, Upgrade\r\n" +
		"Upgrade: websocket\r\n" +
		"Sec-WebSocket-Key: " + key + "\r\n" +
		"Sec-WebSocket-Version: 13\r\n" +
		"\r\n"
}

func maskFrame(payload []byte, mask [4]byte) []byte {
	out := make([]byte, 2+4+len(payload))
	out[0] = 0x81 // FIN + text
	out[1] = 0x80 | byte(len(payload))
	copy(out[2:6], mask[:])
	for i, b := range payload {
		out[6+i] = b ^ mask[i%4]
	}
	return out
}

func assertUpgrade400(t *testing.T, srv *rawhttp.Server, raw string) {
	t.Helper()
	called := false
	h := srv.Handler
	srv.Handler = func(ctx *rawhttp.Ctx) {
		called = true
		if h != nil {
			h(ctx)
		}
	}
	fc := newFakeConn([]byte(raw))
	_ = srv.ServeConn(fc)
	if called {
		t.Fatal("handler must not run")
	}
	if !strings.Contains(fc.w.String(), "400") {
		t.Fatalf("want 400, got %q", fc.w.String())
	}
}

func TestAllowUpgrade_FirefoxConnectionTokens(t *testing.T) {
	key, _ := wsClientKey()
	called := false
	srv := &rawhttp.Server{
		ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
		AllowUpgrade:      true,
		KeepHijackedConns: true,
		Handler: func(ctx *rawhttp.Ctx) {
			called = true
			ctx.SetBody([]byte("ok"))
		},
	}
	fc := newFakeConn([]byte(wsHandshake("/ws", key)))
	_ = srv.ServeConn(fc)
	if !called {
		t.Fatalf("handler must run for keep-alive, Upgrade when AllowUpgrade; resp=%q", fc.w.String())
	}
}

func TestAllowUpgrade_DefaultStillRejects(t *testing.T) {
	key, _ := wsClientKey()
	srv := &rawhttp.Server{
		ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
		Handler: func(ctx *rawhttp.Ctx) {},
	}
	assertUpgrade400(t, srv, wsHandshake("/ws", key))
}

func TestAllowUpgrade_BadKeyLength400(t *testing.T) {
	badKey := base64.StdEncoding.EncodeToString([]byte("shortkey"))
	srv := &rawhttp.Server{
		ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
		AllowUpgrade: true,
	}
	req := "GET /ws HTTP/1.1\r\nHost: localhost\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n" +
		"Sec-WebSocket-Key: " + badKey + "\r\nSec-WebSocket-Version: 13\r\n\r\n"
	assertUpgrade400(t, srv, req)
}

func TestAllowUpgrade_NegativeCorpus(t *testing.T) {
	key, _ := wsClientKey()
	base := func(extra string) string {
		return "GET /ws HTTP/1.1\r\nHost: localhost\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n" +
			"Sec-WebSocket-Key: " + key + "\r\nSec-WebSocket-Version: 13\r\n" + extra + "\r\n"
	}
	cases := []struct {
		name string
		raw  string
	}{
		{"POST", strings.Replace(wsHandshake("/ws", key), "GET ", "POST ", 1)},
		{"HTTP/1.0", strings.Replace(wsHandshake("/ws", key), "HTTP/1.1", "HTTP/1.0", 1)},
		{"with Content-Length", base("Content-Length: 0\r\n")},
		{"with Transfer-Encoding", "GET /ws HTTP/1.1\r\nHost: localhost\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n" +
			"Sec-WebSocket-Key: " + key + "\r\nSec-WebSocket-Version: 13\r\nTransfer-Encoding: chunked\r\n\r\n0\r\n\r\n"},
		{"Upgrade h2c", strings.Replace(wsHandshake("/ws", key), "Upgrade: websocket", "Upgrade: h2c", 1)},
		{"Upgrade websocket, foo", strings.Replace(wsHandshake("/ws", key), "Upgrade: websocket", "Upgrade: websocket, foo", 1)},
		{"double Upgrade", "GET /ws HTTP/1.1\r\nHost: localhost\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nUpgrade: websocket\r\n" +
			"Sec-WebSocket-Key: " + key + "\r\nSec-WebSocket-Version: 13\r\n\r\n"},
		{"duplicate Sec-WebSocket-Key", "GET /ws HTTP/1.1\r\nHost: localhost\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n" +
			"Sec-WebSocket-Key: " + key + "\r\nSec-WebSocket-Key: " + key + "\r\nSec-WebSocket-Version: 13\r\n\r\n"},
		{"missing Version", "GET /ws HTTP/1.1\r\nHost: localhost\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n" +
			"Sec-WebSocket-Key: " + key + "\r\n\r\n"},
		{"Upgrade without Connection upgrade", "GET /ws HTTP/1.1\r\nHost: localhost\r\nConnection: keep-alive\r\nUpgrade: websocket\r\n" +
			"Sec-WebSocket-Key: " + key + "\r\nSec-WebSocket-Version: 13\r\n\r\n"},
		{"Connection upgrade without Upgrade header", "GET /ws HTTP/1.1\r\nHost: localhost\r\nConnection: Upgrade\r\n" +
			"Sec-WebSocket-Key: " + key + "\r\nSec-WebSocket-Version: 13\r\n\r\n"},
	}
	srv := &rawhttp.Server{ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1, AllowUpgrade: true}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertUpgrade400(t, srv, tc.raw)
		})
	}
}

func TestAllowUpgrade_NoHijackClosesConnection(t *testing.T) {
	key, _ := wsClientKey()
	frame := maskFrame([]byte("x"), [4]byte{1, 2, 3, 4})
	var n atomic.Int32
	srv := &rawhttp.Server{
		ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
		AllowUpgrade: true,
		Handler: func(ctx *rawhttp.Ctx) {
			n.Add(1)
			ctx.SetBody([]byte("not-hijacked"))
		},
	}
	// Handshake + second HTTP-looking bytes: must not become a second request.
	payload := append([]byte(wsHandshake("/ws", key)), frame...)
	payload = append(payload, []byte("GET /second HTTP/1.1\r\nHost: localhost\r\n\r\n")...)
	fc := newFakeConn(payload)
	_ = srv.ServeConn(fc)
	if n.Load() != 1 {
		t.Fatalf("want exactly 1 handler call (no keep-alive parse), got %d; resp=%q", n.Load(), fc.w.String())
	}
	if !strings.Contains(fc.w.String(), "Connection: close") {
		t.Fatalf("want Connection: close when Upgrade not Hijack'd; resp=%q", fc.w.String())
	}
}

func TestAllowUpgrade_WebSocketEchoE2E(t *testing.T) {
	key, _ := wsClientKey()
	mask := [4]byte{1, 2, 3, 4}
	payload := []byte("ping")
	frame := maskFrame(payload, mask)

	srv := &rawhttp.Server{
		ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
		AllowUpgrade:      true,
		KeepHijackedConns: true,
		Handler: func(ctx *rawhttp.Ctx) {
			conn, leftover, err := ctx.Hijack()
			if err != nil {
				t.Errorf("Hijack: %v", err)
				return
			}
			accept := wsAccept(string(ctx.Header("Sec-WebSocket-Key")))
			resp := "HTTP/1.1 101 Switching Protocols\r\n" +
				"Upgrade: websocket\r\n" +
				"Connection: Upgrade\r\n" +
				"Sec-WebSocket-Accept: " + accept + "\r\n\r\n"
			if _, err := io.WriteString(conn, resp); err != nil {
				t.Errorf("write 101: %v", err)
				return
			}
			buf := append([]byte(nil), leftover...)
			for _, c := range leftover {
				if c == 0xDE {
					t.Errorf("leftover contains poison 0xDE: %x", leftover)
					break
				}
			}
			for len(buf) < 2+4+len(payload) {
				tmp := make([]byte, 64)
				n, err := conn.Read(tmp)
				if n > 0 {
					buf = append(buf, tmp[:n]...)
				}
				if err != nil {
					break
				}
			}
			if len(buf) < 6+len(payload) {
				t.Errorf("short frame: %x", buf)
				return
			}
			gotMask := buf[2:6]
			got := make([]byte, len(payload))
			for i := range got {
				got[i] = buf[6+i] ^ gotMask[i%4]
			}
			if string(got) != string(payload) {
				t.Errorf("payload %q want %q", got, payload)
			}
			echo := []byte{0x81, byte(len(got))}
			echo = append(echo, got...)
			_, _ = conn.Write(echo)
			_ = conn.Close()
		},
	}

	fc := newFakeConn(append([]byte(wsHandshake("/ws", key)), frame...))
	_ = srv.ServeConn(fc)
	out := fc.w.Bytes()
	if !strings.Contains(string(out), "101") {
		t.Fatalf("want 101, got %q", out)
	}
	if !strings.Contains(string(out), "Sec-WebSocket-Accept: "+wsAccept(key)) {
		t.Fatalf("missing accept in %q", out)
	}
	idx := strings.Index(string(out), "\r\n\r\n")
	if idx < 0 {
		t.Fatal("no header end")
	}
	body := out[idx+4:]
	if len(body) < 2+len(payload) || body[0] != 0x81 || int(body[1]) != len(payload) {
		t.Fatalf("bad echo frame %x", body)
	}
	if string(body[2:2+len(payload)]) != string(payload) {
		t.Fatalf("echo payload %q", body[2:])
	}
}

func TestAllowUpgrade_HijackLeftoverFrame(t *testing.T) {
	key, _ := wsClientKey()
	mask := [4]byte{9, 8, 7, 6}
	payload := []byte("hi")
	frame := maskFrame(payload, mask)

	var leftoverLen int
	var leftoverHasFrame bool
	srv := &rawhttp.Server{
		ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
		AllowUpgrade:      true,
		KeepHijackedConns: true,
		Handler: func(ctx *rawhttp.Ctx) {
			conn, leftover, err := ctx.Hijack()
			if err != nil {
				t.Errorf("Hijack: %v", err)
				return
			}
			for _, c := range leftover {
				if c == 0xDE {
					t.Errorf("leftover poisoned: %x", leftover)
					break
				}
			}
			leftoverLen = len(leftover)
			leftoverHasFrame = len(leftover) >= len(frame)
			if leftoverHasFrame {
				for i := range frame {
					if leftover[i] != frame[i] {
						leftoverHasFrame = false
						break
					}
				}
			}
			_, _ = io.WriteString(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
			_ = conn.Close()
		},
	}
	fc := newFakeConn(append([]byte(wsHandshake("/ws", key)), frame...))
	_ = srv.ServeConn(fc)
	if leftoverLen == 0 {
		t.Fatal("Hijack leftover empty — buffered post-handshake bytes were lost")
	}
	if !leftoverHasFrame {
		t.Fatalf("leftover did not contain the pipelined frame (len=%d)", leftoverLen)
	}
}

func TestAllowUpgrade_HijackClearsDeadlines(t *testing.T) {
	key, _ := wsClientKey()
	mask := [4]byte{1, 2, 3, 4}
	payload := []byte("late")
	frame := maskFrame(payload, mask)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	srv := &rawhttp.Server{
		ReadTimeout:  200 * time.Millisecond,
		WriteTimeout: 200 * time.Millisecond,
		IdleTimeout:  200 * time.Millisecond,
		AllowUpgrade: true,
		KeepHijackedConns: true,
		Handler: func(ctx *rawhttp.Ctx) {
			conn, leftover, err := ctx.Hijack()
			if err != nil {
				t.Errorf("Hijack: %v", err)
				return
			}
			_, _ = io.WriteString(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
			time.Sleep(600 * time.Millisecond)
			buf := append([]byte(nil), leftover...)
			_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			for len(buf) < len(frame) {
				tmp := make([]byte, 64)
				n, err := conn.Read(tmp)
				if n > 0 {
					buf = append(buf, tmp[:n]...)
				}
				if err != nil {
					t.Errorf("read after 600ms (deadline should be cleared on Hijack): %v", err)
					_ = conn.Close()
					return
				}
			}
			echo := []byte{0x81, byte(len(payload))}
			echo = append(echo, payload...)
			_, _ = conn.Write(echo)
			_ = conn.Close()
		},
	}
	go srv.Serve(ln) //nolint:errcheck
	defer srv.Close()

	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := io.WriteString(c, wsHandshake("/ws", key)); err != nil {
		t.Fatal(err)
	}
	// Read 101
	buf := make([]byte, 512)
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _ := c.Read(buf)
	if !strings.Contains(string(buf[:n]), "101") {
		t.Fatalf("want 101, got %q", buf[:n])
	}
	time.Sleep(600 * time.Millisecond)
	if _, err := c.Write(frame); err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err = c.Read(buf)
	if err != nil {
		t.Fatalf("echo read: %v", err)
	}
	if n < 2+len(payload) || buf[0] != 0x81 {
		t.Fatalf("bad echo %x", buf[:n])
	}
}

func TestAllowUpgrade_ConcurrencySlotReleasedAfterHijack(t *testing.T) {
	key, _ := wsClientKey()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	var hijacked net.Conn
	var mu sync.Mutex
	ready := make(chan struct{})
	srv := &rawhttp.Server{
		ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
		Concurrency:       1,
		MaxConnsPerIP:     1,
		AllowUpgrade:      true,
		KeepHijackedConns: true,
		Handler: func(ctx *rawhttp.Ctx) {
			if len(ctx.Header("Upgrade")) == 0 {
				ctx.SetBody([]byte("ok"))
				return
			}
			conn, _, err := ctx.Hijack()
			if err != nil {
				t.Errorf("Hijack: %v", err)
				return
			}
			_, _ = io.WriteString(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
			mu.Lock()
			hijacked = conn
			mu.Unlock()
			close(ready)
		},
	}
	go srv.Serve(ln) //nolint:errcheck
	defer srv.Close()

	c1, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(c1, wsHandshake("/ws", key)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ready:
	case <-time.After(3 * time.Second):
		t.Fatal("hijack timeout")
	}
	time.Sleep(50 * time.Millisecond) // allow defer to release slots

	c2, err := net.DialTimeout("tcp", ln.Addr().String(), time.Second)
	if err != nil {
		t.Fatalf("second conn should be accepted after hijack slot release: %v", err)
	}
	defer c2.Close()
	if _, err := io.WriteString(c2, "GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 256)
	_ = c2.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := c2.Read(buf)
	if err != nil {
		t.Fatalf("second request read: %v", err)
	}
	if !strings.Contains(string(buf[:n]), "200") {
		t.Fatalf("want 200 on second conn after slot release, got %q", buf[:n])
	}
	mu.Lock()
	_ = hijacked.Close()
	mu.Unlock()
	_ = c1.Close()
}

func TestAllowUpgrade_ShutdownDoesNotWaitOnKeepHijacked(t *testing.T) {
	key, _ := wsClientKey()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	held := make(chan net.Conn, 1)
	srv := &rawhttp.Server{
		ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
		AllowUpgrade:      true,
		KeepHijackedConns: true,
		Handler: func(ctx *rawhttp.Ctx) {
			conn, _, err := ctx.Hijack()
			if err != nil {
				return
			}
			_, _ = io.WriteString(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
			held <- conn
		},
	}
	go srv.Serve(ln) //nolint:errcheck

	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_, _ = io.WriteString(c, wsHandshake("/ws", key))
	var hj net.Conn
	select {
	case hj = <-held:
	case <-time.After(3 * time.Second):
		t.Fatal("no hijack")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("Shutdown waited too long on KeepHijacked conn (%v)", time.Since(start))
	}
	// Hijacked conn still usable (Shutdown did not close it).
	_ = hj.SetWriteDeadline(time.Now().Add(time.Second))
	if _, err := hj.Write([]byte{0x81, 0x00}); err != nil {
		t.Fatalf("hijacked conn closed by Shutdown (want left open for handler): %v", err)
	}
	_ = hj.Close()
}
