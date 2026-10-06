package test_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	stdhttp "net/http"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zatrano/rawhttp"
)

func TestLingerLetsWriterRead413(t *testing.T) {
	addr := start413Server(t, 32)
	for i := 0; i < 200; i++ {
		code, err := postSequential(addr, 10<<20)
		if err != nil || code != 413 {
			t.Fatalf("i=%d code=%d err=%v", i, code, err)
		}
	}
}

func TestLingerDelayedReadSees413(t *testing.T) {
	addr := start413Server(t, 32)
	for i := 0; i < 200; i++ {
		code, err := postDelayed(addr, 10<<20, 50*time.Millisecond)
		if err != nil || code != 413 {
			t.Fatalf("i=%d code=%d err=%v", i, code, err)
		}
	}
}

func TestLingerDrainStopsAndGoroutineEnds(t *testing.T) {
	conn := &countConn{}
	srv := &rawhttp.Server{
		ReadTimeout:        -1,
		WriteTimeout:       -1,
		IdleTimeout:        -1,
		MaxRequestBodySize: 32,
		LingerTimeout:      200 * time.Millisecond,
		Handler:            func(*rawhttp.Ctx) { t.Error("handler") },
	}
	conn.prefix = []byte("POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 10485760\r\nConnection: close\r\n\r\n")
	start := time.Now()
	err := srv.ServeConn(conn)
	elapsed := time.Since(start)
	if err != rawhttp.ErrBodyTooLarge {
		t.Fatalf("err=%v", err)
	}
	if gotn := conn.read.Load(); gotn > 256<<10 {
		t.Fatalf("drained %d", gotn)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("elapsed %s", elapsed)
	}
	if !bytes.Contains(conn.w.Bytes(), []byte("413")) {
		t.Fatalf("resp=%q", conn.w.Bytes())
	}
}

func TestLingerSlowClientHitsTimeout(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := make(chan struct{})
	var once sync.Once
	srv := &rawhttp.Server{
		ReadTimeout:        -1,
		WriteTimeout:       -1,
		IdleTimeout:        -1,
		MaxRequestBodySize: 32,
		Handler:            func(*rawhttp.Ctx) { t.Error("handler") },
		ConnState: func(_ net.Conn, st rawhttp.ConnState) {
			if st == rawhttp.StateClosed {
				once.Do(func() { close(closed) })
			}
		},
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { shutdownSrv(srv) })
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := io.WriteString(conn, "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 1000000\r\nConnection: close\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	go func() {
		buf := []byte{'x'}
		for {
			time.Sleep(50 * time.Millisecond)
			if _, err := conn.Write(buf); err != nil {
				return
			}
		}
	}()
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("goroutine still running")
	}
	if elapsed := time.Since(start); elapsed > 1300*time.Millisecond {
		t.Fatalf("elapsed %s", elapsed)
	}
}

func TestLingerDropsPipelinedNextRequest(t *testing.T) {
	var hit atomic.Int32
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &rawhttp.Server{
		ReadTimeout:        -1,
		WriteTimeout:       -1,
		IdleTimeout:        -1,
		MaxRequestBodySize: 32,
		Handler:            func(*rawhttp.Ctx) { hit.Add(1) },
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { shutdownSrv(srv) })
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	raw := "POST /first HTTP/1.1\r\nHost: localhost\r\nContent-Length: 1000000\r\n\r\nGET /second HTTP/1.1\r\nHost: localhost\r\n\r\n"
	go func() { _, _ = io.WriteString(conn, raw) }()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	resp, err := stdhttp.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 413 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	_ = conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	second, err := stdhttp.ReadResponse(bufio.NewReader(conn), nil)
	if err == nil {
		t.Fatalf("pipelined status=%d", second.StatusCode)
	}
	if hit.Load() != 0 {
		t.Fatalf("handler hits=%d", hit.Load())
	}
}

func TestLingerTLSSees413(t *testing.T) {
	certPEM, keyPEM := mustTestCert(t)
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tlsLn := tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{cert}})
	srv := &rawhttp.Server{
		ReadTimeout:        -1,
		WriteTimeout:       -1,
		IdleTimeout:        -1,
		MaxRequestBodySize: 32,
		Handler:            func(*rawhttp.Ctx) { t.Error("handler") },
	}
	go func() { _ = srv.Serve(tlsLn) }()
	t.Cleanup(func() { shutdownSrv(srv) })
	for i := 0; i < 20; i++ {
		code, err := postSequentialTLS(ln.Addr().String(), 10<<20)
		if err != nil || code != 413 {
			t.Fatalf("i=%d code=%d err=%v", i, code, err)
		}
	}
}

func TestLingerNoGoroutineLeak(t *testing.T) {
	addr := start413Server(t, 32)
	before := runtime.NumGoroutine()
	for i := 0; i < 30; i++ {
		code, err := postSequential(addr, 1<<20)
		if err != nil || code != 413 {
			t.Fatalf("i=%d code=%d err=%v", i, code, err)
		}
	}
	time.Sleep(1500 * time.Millisecond)
	if after := runtime.NumGoroutine(); after > before+8 {
		t.Fatalf("goroutines %d -> %d", before, after)
	}
}

func start413Server(t *testing.T, maxBody int) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &rawhttp.Server{
		ReadTimeout:        -1,
		WriteTimeout:       -1,
		IdleTimeout:        -1,
		MaxRequestBodySize: maxBody,
		Handler:            func(*rawhttp.Ctx) { t.Error("handler") },
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { shutdownSrv(srv) })
	return ln.Addr().String()
}

func shutdownSrv(srv *rawhttp.Server) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}

func postSequential(addr string, size int) (int, error) {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	return writeLimitedThenStatus(conn, size)
}

func postSequentialTLS(addr string, size int) (int, error) {
	conn, err := tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	return writeLimitedThenStatus(conn, size)
}

func postDelayed(addr string, size int, delay time.Duration) (int, error) {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.WriteString(conn, fmt.Sprintf("POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: %d\r\nConnection: close\r\n\r\n", size)); err != nil {
		return 0, err
	}
	go func() { _ = writeChunks(conn, size) }()
	time.Sleep(delay)
	return readStatus(conn)
}

func writeLimitedThenStatus(conn net.Conn, size int) (int, error) {
	if _, err := io.WriteString(conn, fmt.Sprintf("POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: %d\r\nConnection: close\r\n\r\n", size)); err != nil {
		return 0, err
	}
	_ = conn.SetWriteDeadline(time.Now().Add(40 * time.Millisecond))
	_ = writeChunks(conn, size)
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	return readStatus(conn)
}

func writeChunks(conn net.Conn, size int) error {
	chunk := bytes.Repeat([]byte("a"), 64<<10)
	left := size
	for left > 0 {
		n := len(chunk)
		if n > left {
			n = left
		}
		if _, err := conn.Write(chunk[:n]); err != nil {
			return err
		}
		left -= n
	}
	return nil
}

func readStatus(conn net.Conn) (int, error) {
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	resp, err := stdhttp.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}

type countConn struct {
	prefix []byte
	off    int
	read   atomic.Int64
	w      bytes.Buffer
	mu     sync.Mutex
}

func (c *countConn) Read(b []byte) (int, error) {
	if c.off < len(c.prefix) {
		n := copy(b, c.prefix[c.off:])
		c.off += n
		return n, nil
	}
	if c.read.Load() >= 1<<20 {
		return 0, io.EOF
	}
	n := len(b)
	if n > 16<<10 {
		n = 16 << 10
	}
	c.read.Add(int64(n))
	return n, nil
}

func (c *countConn) Write(b []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.w.Write(b)
}

func (c *countConn) Close() error                     { return nil }
func (c *countConn) LocalAddr() net.Addr              { return dummyAddr{} }
func (c *countConn) RemoteAddr() net.Addr             { return dummyAddr{} }
func (c *countConn) SetDeadline(time.Time) error      { return nil }
func (c *countConn) SetReadDeadline(time.Time) error  { return nil }
func (c *countConn) SetWriteDeadline(time.Time) error { return nil }
func (c *countConn) CloseWrite() error                { return nil }
