package rawhttp

import (
	"bytes"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

type scriptConn struct {
	mu     sync.Mutex
	parts  [][]byte
	part   int
	off    int
	w      bytes.Buffer
	delay  time.Duration
	chunk  int
	closed bool
}

func (c *scriptConn) Read(b []byte) (int, error) {
	if c.delay > 0 {
		time.Sleep(c.delay)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.part >= len(c.parts) {
		return 0, io.EOF
	}
	p := c.parts[c.part]
	n := len(p) - c.off
	if c.chunk > 0 && n > c.chunk {
		n = c.chunk
	}
	if n > len(b) {
		n = len(b)
	}
	copy(b[:n], p[c.off:c.off+n])
	c.off += n
	if c.off >= len(p) {
		c.part++
		c.off = 0
	}
	if n == 0 {
		return 0, io.EOF
	}
	return n, nil
}

func (c *scriptConn) Write(b []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.w.Write(b)
}

func (c *scriptConn) Close() error {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	return nil
}

func (c *scriptConn) LocalAddr() net.Addr  { return scriptAddr{} }
func (c *scriptConn) RemoteAddr() net.Addr { return scriptAddr{} }

func (c *scriptConn) SetDeadline(t time.Time) error {
	_ = c.SetReadDeadline(t)
	return c.SetWriteDeadline(t)
}

func (c *scriptConn) SetReadDeadline(time.Time) error  { return nil }
func (c *scriptConn) SetWriteDeadline(time.Time) error { return nil }

func (c *scriptConn) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.w.String()
}

type scriptAddr struct{}

func (scriptAddr) Network() string { return "tcp" }
func (scriptAddr) String() string  { return "127.0.0.1:1" }

func noDeadlineServer(h Handler) *Server {
	return &Server{
		Handler:           h,
		ReadTimeout:       -1,
		WriteTimeout:      -1,
		IdleTimeout:       -1,
		ReadHeaderTimeout: -1,
	}
}

func TestDefaultReadBufferStartsAt4KiB(t *testing.T) {
	var capSeen int
	srv := noDeadlineServer(func(ctx *Ctx) {
		capSeen = cap(ctx.cr.buf)
		ctx.SetBodyString("ok")
	})
	req := "GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
	fc := &scriptConn{parts: [][]byte{[]byte(req)}}
	if err := srv.ServeConn(fc); err != nil && !strings.Contains(err.Error(), "EOF") {
		t.Fatal(err)
	}
	if capSeen != readBufInitial {
		t.Fatalf("cap=%d want %d", capSeen, readBufInitial)
	}
}

func TestDefaultReadBufferGrowsTo8KiB(t *testing.T) {
	payload := strings.Repeat("a", 5<<10)
	req := "GET / HTTP/1.1\r\nHost: localhost\r\nX: " + payload + "\r\nConnection: close\r\n\r\n"
	var capSeen int
	srv := noDeadlineServer(func(ctx *Ctx) {
		capSeen = cap(ctx.cr.buf)
		ctx.SetBodyString("ok")
	})
	fc := &scriptConn{parts: [][]byte{[]byte(req)}}
	if err := srv.ServeConn(fc); err != nil && !strings.Contains(err.Error(), "EOF") {
		t.Fatal(err)
	}
	if capSeen != readBufStep8 {
		t.Fatalf("cap=%d want %d", capSeen, readBufStep8)
	}
}

func TestReadBufferStartsAt4KiB(t *testing.T) {
	var capSeen int
	srv := noDeadlineServer(func(ctx *Ctx) {
		capSeen = cap(ctx.cr.buf)
		ctx.SetBodyString("ok")
	})
	srv.MaxHeaderBytes = 16 << 10
	req := "GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
	fc := &scriptConn{parts: [][]byte{[]byte(req)}}
	if err := srv.ServeConn(fc); err != nil && !strings.Contains(err.Error(), "EOF") {
		t.Fatal(err)
	}
	if capSeen != readBufInitial {
		t.Fatalf("cap=%d want %d", capSeen, readBufInitial)
	}
	if !strings.Contains(fc.String(), "200") {
		t.Fatalf("resp=%q", fc.String())
	}
}

func TestReadBufferGrowsForFragmentedHeader(t *testing.T) {
	payload := strings.Repeat("a", 6<<10)
	req := "GET / HTTP/1.1\r\nHost: localhost\r\nX: " + payload + "\r\nConnection: close\r\n\r\n"
	var got string
	var capSeen int
	srv := noDeadlineServer(func(ctx *Ctx) {
		capSeen = cap(ctx.cr.buf)
		got = headerValue(ctx, "X")
		ctx.SetBodyString("ok")
	})
	srv.MaxHeaderBytes = 16 << 10
	fc := &scriptConn{parts: [][]byte{[]byte(req)}, chunk: 64}
	if err := srv.ServeConn(fc); err != nil && !strings.Contains(err.Error(), "EOF") {
		t.Fatal(err)
	}
	if capSeen != readBufStep8 {
		t.Fatalf("cap=%d want %d", capSeen, readBufStep8)
	}
	if got != payload || !strings.Contains(fc.String(), "200") {
		t.Fatalf("gotLen=%d resp=%q", len(got), fc.String()[:min(80, len(fc.String()))])
	}
}

func TestReadBufferTruncatedHeader(t *testing.T) {
	srv := noDeadlineServer(func(ctx *Ctx) { t.Fatal("handler") })
	fc := &scriptConn{parts: [][]byte{[]byte("GET / HTTP/1.1\r\nHost: loc")}}
	err := srv.ServeConn(fc)
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(fc.String(), "200") {
		t.Fatalf("resp=%q", fc.String())
	}
}

func TestReadBufferNearAndOver16KiB(t *testing.T) {
	near := strings.Repeat("b", 14<<10)
	req := "GET / HTTP/1.1\r\nHost: localhost\r\nX: " + near + "\r\nConnection: close\r\n\r\n"
	var capSeen int
	var got int
	srv := noDeadlineServer(func(ctx *Ctx) {
		capSeen = cap(ctx.cr.buf)
		got = len(headerValue(ctx, "X"))
		ctx.SetBodyString("ok")
	})
	srv.MaxHeaderBytes = 16 << 10
	fc := &scriptConn{parts: [][]byte{[]byte(req)}, chunk: 1024}
	if err := srv.ServeConn(fc); err != nil && !strings.Contains(err.Error(), "EOF") {
		t.Fatal(err)
	}
	if capSeen != readBufStep16 || got != len(near) || !strings.Contains(fc.String(), "200") {
		t.Fatalf("cap=%d got=%d respHas200=%v", capSeen, got, strings.Contains(fc.String(), "200"))
	}

	over := "GET / HTTP/1.1\r\nHost: localhost\r\nX: " + strings.Repeat("c", 20<<10) + "\r\nConnection: close\r\n\r\n"
	srv2 := noDeadlineServer(func(ctx *Ctx) { t.Fatal("handler") })
	srv2.MaxHeaderBytes = 16 << 10
	fc2 := &scriptConn{parts: [][]byte{[]byte(over)}}
	_ = srv2.ServeConn(fc2)
	if !strings.Contains(fc2.String(), "431") {
		t.Fatalf("resp=%q", fc2.String()[:min(120, len(fc2.String()))])
	}
}

func TestReadBufferPipelinedAndIdleShrink(t *testing.T) {
	big := strings.Repeat("d", 14<<10)
	first := "GET /a HTTP/1.1\r\nHost: localhost\r\nX: " + big + "\r\n\r\n"
	second := "GET /b HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
	var caps []int
	srv := noDeadlineServer(func(ctx *Ctx) {
		caps = append(caps, cap(ctx.cr.buf))
		ctx.SetBodyString("ok")
	})
	srv.MaxHeaderBytes = 16 << 10
	fc := &scriptConn{parts: [][]byte{[]byte(first), []byte(second)}}
	if err := srv.ServeConn(fc); err != nil && !strings.Contains(err.Error(), "EOF") {
		t.Fatal(err)
	}
	if len(caps) != 2 || caps[0] != readBufStep16 || caps[1] != readBufInitial {
		t.Fatalf("caps=%v", caps)
	}
	if strings.Count(fc.String(), "200") != 2 {
		t.Fatalf("resp=%q", fc.String()[:min(200, len(fc.String()))])
	}
}

func TestReadBufferSlowClient(t *testing.T) {
	req := "GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
	var ok bool
	srv := &Server{
		Handler: func(ctx *Ctx) {
			ok = true
			ctx.SetBodyString("ok")
		},
		ReadTimeout:       2 * time.Second,
		WriteTimeout:      2 * time.Second,
		IdleTimeout:       2 * time.Second,
		ReadHeaderTimeout: 2 * time.Second,
	}
	fc := &scriptConn{parts: [][]byte{[]byte(req)}, chunk: 8, delay: 15 * time.Millisecond}
	if err := srv.ServeConn(fc); err != nil && !strings.Contains(err.Error(), "EOF") {
		t.Fatal(err)
	}
	if !ok || !strings.Contains(fc.String(), "200") {
		t.Fatalf("ok=%v resp=%q", ok, fc.String())
	}
}

func TestGrownReadPoolIsCapped(t *testing.T) {
	grown16.mu.Lock()
	grown16.n = 0
	grown16.mu.Unlock()
	t.Cleanup(func() {
		grown16.mu.Lock()
		grown16.n = 0
		for i := range grown16.bufs {
			grown16.bufs[i] = nil
		}
		grown16.mu.Unlock()
	})
	for i := 0; i < grownReadPoolMax+10; i++ {
		grown16.put(make([]byte, readBufStep16))
	}
	grown16.mu.Lock()
	n := grown16.n
	grown16.mu.Unlock()
	if n != grownReadPoolMax {
		t.Fatalf("pool=%d want %d", n, grownReadPoolMax)
	}
}

func headerValue(ctx *Ctx, name string) string {
	block := ctx.headerBlock
	key := strings.ToLower(name)
	for len(block) > 0 {
		line, rest, ok := bytes.Cut(block, []byte("\r\n"))
		if !ok {
			break
		}
		block = rest
		k, v, found := bytes.Cut(line, []byte(":"))
		if !found {
			continue
		}
		if strings.EqualFold(string(k), key) {
			return strings.TrimSpace(string(v))
		}
	}
	return ""
}
