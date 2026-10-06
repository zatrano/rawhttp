package test_test

import (
	"context"
	"errors"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zatrano/rawhttp"
)

type deadlineConn struct {
	net.Conn
	reads  atomic.Int32
	writes atomic.Int32
}

func (c *deadlineConn) SetReadDeadline(t time.Time) error {
	c.reads.Add(1)
	return c.Conn.SetReadDeadline(t)
}

func (c *deadlineConn) SetWriteDeadline(t time.Time) error {
	c.writes.Add(1)
	return c.Conn.SetWriteDeadline(t)
}

func (c *deadlineConn) SetDeadline(t time.Time) error {
	c.reads.Add(1)
	c.writes.Add(1)
	return c.Conn.SetDeadline(t)
}

type wrapListener struct {
	net.Listener
	wrap func(net.Conn) net.Conn
}

func (l wrapListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	if l.wrap != nil {
		c = l.wrap(c)
	}
	return c, nil
}

func TestShutdownLeavesHijackedReadAlone(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan *deadlineConn, 1)
	readDone := make(chan struct{})
	srv := &rawhttp.Server{
		KeepHijackedConns: true,
		ReadTimeout:       -1,
		WriteTimeout:      -1,
		IdleTimeout:       -1,
		Handler: func(ctx *rawhttp.Ctx) {
			conn, _, herr := ctx.Hijack()
			if herr != nil {
				t.Errorf("hijack: %v", herr)
				return
			}
			dc := conn.(*deadlineConn)
			dc.reads.Store(0)
			dc.writes.Store(0)
			entered <- dc
			buf := make([]byte, 1)
			_, _ = conn.Read(buf)
			close(readDone)
		},
	}
	wln := &wrapListener{Listener: ln, wrap: func(c net.Conn) net.Conn {
		return &deadlineConn{Conn: c}
	}}
	go srv.Serve(wln) //nolint:errcheck
	t.Cleanup(func() { _ = srv.Close() })

	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := io.WriteString(c, "GET / HTTP/1.1\r\nHost: localhost\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	var dc *deadlineConn
	select {
	case dc = <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not hijack")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	err = srv.Shutdown(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown = %v, want DeadlineExceeded", err)
	}
	if time.Since(start) < 150*time.Millisecond {
		t.Fatalf("Shutdown returned too fast (%s); hijacked handler should keep it waiting", time.Since(start))
	}
	if dc.reads.Load() != 0 || dc.writes.Load() != 0 {
		t.Fatalf("deadlines during Shutdown reads=%d writes=%d", dc.reads.Load(), dc.writes.Load())
	}
	select {
	case <-readDone:
		t.Fatal("hijacked Read returned during Shutdown")
	default:
	}
}

func TestShutdownDoesNotBreakHijackCloseExchange(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan byte, 1)
	srv := &rawhttp.Server{
		KeepHijackedConns: true,
		ReadTimeout:       -1,
		WriteTimeout:      -1,
		IdleTimeout:       -1,
		Handler: func(ctx *rawhttp.Ctx) {
			conn, _, herr := ctx.Hijack()
			if herr != nil {
				return
			}
			_, _ = conn.Write([]byte{0x88, 0x00})
			buf := make([]byte, 1)
			n, rerr := conn.Read(buf)
			if rerr != nil || n != 1 {
				got <- 0
				return
			}
			got <- buf[0]
		},
	}
	go srv.Serve(ln) //nolint:errcheck
	t.Cleanup(func() { _ = srv.Close() })

	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.WriteString(c, "GET / HTTP/1.1\r\nHost: localhost\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	frame := make([]byte, 2)
	if _, err := io.ReadFull(c, frame); err != nil {
		t.Fatal(err)
	}
	if frame[0] != 0x88 || frame[1] != 0x00 {
		t.Fatalf("close frame %x", frame)
	}
	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		done <- srv.Shutdown(ctx)
	}()
	time.Sleep(50 * time.Millisecond)
	if _, err := c.Write([]byte{0x07}); err != nil {
		t.Fatal(err)
	}
	select {
	case b := <-got:
		if b != 0x07 {
			t.Fatalf("handler read %d", b)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handler Read was interrupted")
	}
	if err := <-done; err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestCloseDropsHijackedConnStillInHandler(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	readDone := make(chan error, 1)
	srv := &rawhttp.Server{
		KeepHijackedConns: true,
		ReadTimeout:       -1,
		WriteTimeout:      -1,
		IdleTimeout:       -1,
		Handler: func(ctx *rawhttp.Ctx) {
			conn, _, herr := ctx.Hijack()
			if herr != nil {
				readDone <- herr
				return
			}
			close(entered)
			buf := make([]byte, 1)
			_, rerr := conn.Read(buf)
			readDone <- rerr
		},
	}
	go srv.Serve(ln) //nolint:errcheck

	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := io.WriteString(c, "GET / HTTP/1.1\r\nHost: localhost\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not hijack")
	}
	if err := srv.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-readDone:
		if err == nil {
			t.Fatal("Close left the in-handler hijacked conn readable")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not unblock the hijacked Read")
	}
}

func TestCloseLeavesReturnedHijackOpen(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	held := make(chan net.Conn, 1)
	srv := &rawhttp.Server{
		KeepHijackedConns: true,
		ReadTimeout:       -1,
		WriteTimeout:      -1,
		IdleTimeout:       -1,
		Handler: func(ctx *rawhttp.Ctx) {
			conn, _, herr := ctx.Hijack()
			if herr != nil {
				return
			}
			held <- conn
		},
	}
	go srv.Serve(ln) //nolint:errcheck

	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := io.WriteString(c, "GET / HTTP/1.1\r\nHost: localhost\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	var hj net.Conn
	select {
	case hj = <-held:
	case <-time.After(2 * time.Second):
		t.Fatal("no hijack")
	}
	time.Sleep(50 * time.Millisecond)
	if err := srv.Close(); err != nil {
		t.Fatal(err)
	}
	_ = hj.SetWriteDeadline(time.Now().Add(time.Second))
	if _, err := hj.Write([]byte("open")); err != nil {
		t.Fatalf("Close closed a hijacked conn whose handler had returned: %v", err)
	}
	_ = hj.Close()
}
