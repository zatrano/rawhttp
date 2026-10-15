package test_test

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net"
	"os"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zatrano/rawhttp"
)

// refreshGranule is deadlineSlack: min(d/8, 1s). A kept deadline is at most
// this much earlier than now+d. It is never later.
func refreshGranule(d time.Duration) time.Duration {
	s := d / 8
	if s > time.Second {
		return time.Second
	}
	return s
}

// countDL counts deadline syscalls on one accepted connection.
type countDL struct {
	net.Conn
	reads  atomic.Int32
	writes atomic.Int32
}

func (c *countDL) SetReadDeadline(t time.Time) error {
	c.reads.Add(1)
	return c.Conn.SetReadDeadline(t)
}

func (c *countDL) SetWriteDeadline(t time.Time) error {
	c.writes.Add(1)
	return c.Conn.SetWriteDeadline(t)
}

func (c *countDL) SetDeadline(t time.Time) error {
	c.reads.Add(1)
	c.writes.Add(1)
	return c.Conn.SetDeadline(t)
}

// A keep-alive burst whose headers arrive in one read must not re-arm a
// still-valid idle or write deadline, and must not tighten the header
// deadline when the header block is already buffered.
func TestKeepAliveCoalescesFreshDeadlines(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	srv := &rawhttp.Server{
		ReadHeaderTimeout: time.Second,
		ReadTimeout:       time.Second,
		WriteTimeout:      time.Second,
		IdleTimeout:       2 * time.Second,
		Handler:           func(ctx *rawhttp.Ctx) { ctx.SetBodyString("ok") },
	}
	wrapped := make(chan *countDL, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		dc := &countDL{Conn: c}
		wrapped <- dc
		_ = srv.ServeConn(dc)
	}()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	const n = 8
	req := "GET / HTTP/1.1\r\nHost: localhost\r\nConnection: keep-alive\r\n\r\n"
	var burst []byte
	for i := 0; i < n; i++ {
		burst = append(burst, req...)
	}
	if _, err := conn.Write(burst); err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(conn)
	for i := 0; i < n; i++ {
		if err := discardResponse(br); err != nil {
			t.Fatalf("response %d: %v", i, err)
		}
	}
	_ = conn.Close()

	var dc *countDL
	select {
	case dc = <-wrapped:
	case <-time.After(2 * time.Second):
		t.Fatal("accept did not wrap the connection")
	}
	// One arm for the idle wait and one for the write window. A second
	// refresh inside the slack window means the coalesce missed.
	if got := dc.reads.Load(); got > 2 {
		t.Fatalf("read deadlines armed %d times for %d buffered requests", got, n)
	}
	if got := dc.writes.Load(); got > 2 {
		t.Fatalf("write deadlines armed %d times for %d buffered requests", got, n)
	}
}

// An idle keep-alive connection closes on IdleTimeout. This test sets
// IdleTimeout equal to ReadTimeout, so the close is inside ReadTimeout plus
// the refresh granule. The wait between requests is IdleTimeout (default 90s
// when IdleTimeout is 0), not ReadTimeout.
func TestIdleKeepAliveClosesWithinReadTimeoutPlusGranule(t *testing.T) {
	const readTO = 400 * time.Millisecond
	granule := refreshGranule(readTO)
	ln, srv := serveDeadline(t, &rawhttp.Server{
		ReadHeaderTimeout: readTO,
		ReadTimeout:       readTO,
		WriteTimeout:      2 * time.Second,
		IdleTimeout:       readTO,
		Handler:           func(ctx *rawhttp.Ctx) { ctx.SetBodyString("ok") },
	})
	defer ln.Close()
	defer srv.Close()

	conn := dialDeadline(t, ln)
	defer conn.Close()
	if _, err := conn.Write([]byte("GET / HTTP/1.1\r\nHost: localhost\r\nConnection: keep-alive\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(conn)
	if err := discardResponse(br); err != nil {
		t.Fatal(err)
	}
	t0 := time.Now()
	bound := readTO + granule
	_ = conn.SetReadDeadline(t0.Add(bound + 200*time.Millisecond))
	_, err := conn.Read(make([]byte, 1))
	elapsed := time.Since(t0)
	if err == nil {
		t.Fatal("idle connection delivered another byte")
	}
	if errors.Is(err, os.ErrDeadlineExceeded) || elapsed > bound+150*time.Millisecond {
		t.Fatalf("idle connection still open after %s, bound %s", elapsed, bound)
	}
}

// A connection that sends a request every ReadTimeout/2 stays up for 3× ReadTimeout.
func TestActiveKeepAliveSurvivesThreeReadTimeouts(t *testing.T) {
	const readTO = 400 * time.Millisecond
	ln, srv := serveDeadline(t, &rawhttp.Server{
		ReadHeaderTimeout: readTO,
		ReadTimeout:       readTO,
		WriteTimeout:      2 * time.Second,
		IdleTimeout:       3 * readTO,
		Handler:           func(ctx *rawhttp.Ctx) { ctx.SetBodyString("ok") },
	})
	defer ln.Close()
	defer srv.Close()

	conn := dialDeadline(t, ln)
	defer conn.Close()
	br := bufio.NewReader(conn)
	req := []byte("GET / HTTP/1.1\r\nHost: localhost\r\nConnection: keep-alive\r\n\r\n")
	start := time.Now()
	until := start.Add(3 * readTO)
	n := 0
	for {
		if _, err := conn.Write(req); err != nil {
			t.Fatalf("request %d at %s: %v", n, time.Since(start), err)
		}
		if err := discardResponse(br); err != nil {
			t.Fatalf("response %d at %s: %v", n, time.Since(start), err)
		}
		n++
		if !time.Now().Before(until) {
			break
		}
		next := start.Add(time.Duration(n) * readTO / 2)
		if d := time.Until(next); d > 0 {
			time.Sleep(d)
		}
	}
	if time.Since(start) < 3*readTO {
		t.Fatalf("finished at %s after %d requests", time.Since(start), n)
	}
}

// A reader that does not drain the body is cut by WriteTimeout. The refresh
// granule does not extend that deadline. net.Pipe blocks the response write
// until the client reads it.
func TestSlowReaderClosesAtWriteTimeout(t *testing.T) {
	const writeTO = 400 * time.Millisecond
	granule := refreshGranule(writeTO)
	body := bytes.Repeat([]byte("x"), 64<<10)
	client, server := net.Pipe()
	defer client.Close()
	srv := &rawhttp.Server{
		ReadHeaderTimeout: time.Second,
		ReadTimeout:       2 * time.Second,
		WriteTimeout:      writeTO,
		IdleTimeout:       2 * time.Second,
		Handler:           func(ctx *rawhttp.Ctx) { ctx.SetBody(body) },
	}
	done := make(chan error, 1)
	go func() {
		err := srv.ServeConn(server)
		_ = server.Close()
		done <- err
	}()

	if _, err := client.Write([]byte("GET / HTTP/1.1\r\nHost: localhost\r\nConnection: keep-alive\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	t0 := time.Now()
	buf := make([]byte, 256)
	_ = client.SetReadDeadline(t0.Add(time.Second))
	if _, err := client.Read(buf); err != nil {
		t.Fatal(err)
	}
	bound := writeTO + granule
	if wait := bound - time.Since(t0); wait > 0 {
		time.Sleep(wait)
	}
	_ = client.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	_, readErr := client.Read(buf)
	elapsed := time.Since(t0)
	var serveErr error
	select {
	case serveErr = <-done:
	default:
	}
	if readErr == nil || errors.Is(readErr, os.ErrDeadlineExceeded) || elapsed > bound+200*time.Millisecond {
		t.Fatalf("slow reader still open after %s, bound %s: %v (serve %v)", elapsed, bound, readErr, serveErr)
	}
}

// The same slow reader on TCP loopback. The accepted socket has a small send
// buffer and the client has a small receive buffer, so a large body blocks
// in Write until WriteTimeout on a stack that honors those buffers.
//
// Windows loopback does not. With SO_RCVBUF and SO_SNDBUF at 1024 and
// SIO_LOOPBACK_FAST_PATH disabled, an 8 MiB Write to a peer that does not
// read still returns in about 5 ms. WriteTimeout is not observable there.
// The pipe test covers that case.
func TestSlowReaderClosesAtWriteTimeoutTCP(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows loopback accepts a multi-megabyte write despite 2 KiB socket buffers")
	}
	const writeTO = 400 * time.Millisecond
	granule := refreshGranule(writeTO)
	body := bytes.Repeat([]byte("x"), 8<<20)
	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln := smallSendListener{Listener: base}
	srv := &rawhttp.Server{
		ReadHeaderTimeout: time.Second,
		ReadTimeout:       2 * time.Second,
		WriteTimeout:      writeTO,
		IdleTimeout:       2 * time.Second,
		Handler:           func(ctx *rawhttp.Ctx) { ctx.SetBody(body) },
	}
	go srv.Serve(ln) //nolint:errcheck
	defer ln.Close()
	defer srv.Close()

	dialer := net.Dialer{Control: smallRecvControl}
	conn, err := dialer.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	tightenRead(conn)
	if _, err := conn.Write([]byte("GET / HTTP/1.1\r\nHost: localhost\r\nConnection: keep-alive\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	t0 := time.Now()
	buf := make([]byte, 256)
	_ = conn.SetReadDeadline(t0.Add(time.Second))
	n, err := conn.Read(buf)
	if n == 0 && err != nil {
		t.Fatal(err)
	}
	got := n
	bound := writeTO + granule
	// Stop reading so the small window stays full and the server write blocks.
	if wait := bound - time.Since(t0); wait > 0 {
		time.Sleep(wait)
	}
	stop := time.Now().Add(200 * time.Millisecond)
	var readErr error
	for {
		_ = conn.SetReadDeadline(stop)
		n, readErr = conn.Read(buf)
		got += n
		if readErr != nil {
			break
		}
	}
	elapsed := time.Since(t0)
	// A closed connection still yields the bytes already in the 2 KiB window.
	// A write that finished delivers the 8 MiB body and then stays open.
	if got > 64<<10 || errors.Is(readErr, os.ErrDeadlineExceeded) || elapsed > bound+200*time.Millisecond {
		t.Fatalf("slow TCP reader still open after %s, bound %s, read %d: %v (%s)", elapsed, bound, got, readErr, tcpBufferDetail(conn))
	}
}

func serveDeadline(t *testing.T, srv *rawhttp.Server) (net.Listener, *rawhttp.Server) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln) //nolint:errcheck
	return ln, srv
}

func dialDeadline(t *testing.T, ln net.Listener) net.Conn {
	t.Helper()
	conn, err := net.DialTimeout("tcp", ln.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return conn
}

func discardResponse(br *bufio.Reader) error {
	for {
		line, err := br.ReadSlice('\n')
		if err != nil {
			return err
		}
		if (len(line) == 2 && line[0] == '\r') || (len(line) == 1 && line[0] == '\n') {
			break
		}
	}
	body := make([]byte, 2)
	_, err := io.ReadFull(br, body)
	return err
}
