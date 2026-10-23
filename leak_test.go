package rawhttp_test

import (
	"bufio"
	"context"
	"io"
	"log"
	"net"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zatrano/rawhttp"
)

// goroutinesSettle waits until the goroutine count drops to at most limit.
func goroutinesSettle(limit int, wait time.Duration) int {
	deadline := time.Now().Add(wait)
	for {
		n := runtime.NumGoroutine()
		if n <= limit || time.Now().After(deadline) {
			return n
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func dumpGoroutines() string {
	buf := make([]byte, 1<<20)
	return string(buf[:runtime.Stack(buf, true)])
}

// After Shutdown, no connection goroutine survives, whatever state the
// connections were in: idle keep-alive, half-sent headers, half-sent body,
// a handler panic, a parse error or a streamed body left unread.
func TestNoGoroutineLeakAfterShutdown(t *testing.T) {
	before := runtime.NumGoroutine()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &rawhttp.Server{
		ReadTimeout:       2 * time.Second,
		IdleTimeout:       2 * time.Second,
		StreamRequestBody: true,
		ErrorLog:          log.New(io.Discard, "", 0),
		Handler: func(ctx *rawhttp.Ctx) {
			switch string(ctx.Path) {
			case "/panic":
				panic("leak-test")
			case "/slow":
				time.Sleep(50 * time.Millisecond)
			}
			ctx.SetBodyString("ok")
		},
	}
	served := make(chan struct{})
	go func() { _ = srv.Serve(ln); close(served) }()
	addr := ln.Addr().String()

	scripts := []string{
		"GET / HTTP/1.1\r\nHost: x\r\n\r\n",
		"GET / HTTP/1.1\r\nHost: x\r\n",
		"POST / HTTP/1.1\r\nHost: x\r\nContent-Length: 100\r\n\r\nabc",
		"GET /panic HTTP/1.1\r\nHost: x\r\n\r\n",
		"GET /bad HTTP/1.1\r\nHost: x\r\nContent-Length: 1\r\nContent-Length: 2\r\n\r\n",
		"POST /slow HTTP/1.1\r\nHost: x\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nhello\r\n",
		"G",
	}
	var conns []net.Conn
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		for _, s := range scripts {
			wg.Add(1)
			go func(s string) {
				defer wg.Done()
				c, err := net.DialTimeout("tcp", addr, 2*time.Second)
				if err != nil {
					return
				}
				_, _ = io.WriteString(c, s)
				mu.Lock()
				conns = append(conns, c)
				mu.Unlock()
			}(s)
		}
	}
	wg.Wait()
	time.Sleep(200 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	_ = srv.Shutdown(ctx)
	cancel()
	_ = srv.Close()
	<-served
	for _, c := range conns {
		_ = c.Close()
	}

	if n := goroutinesSettle(before+2, 5*time.Second); n > before+2 {
		t.Fatalf("goroutines %d -> %d after Shutdown\n%s", before, n, dumpGoroutines())
	}
	if open := srv.OpenConnections.Load(); open != 0 {
		t.Fatalf("OpenConnections=%d after Shutdown", open)
	}
}

// Many short keep-alive clients that disconnect mid-stream leave nothing
// behind once they are gone.
func TestNoGoroutineLeakAfterClientDisconnects(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &rawhttp.Server{
		ReadTimeout:   2 * time.Second,
		WriteTimeout:  2 * time.Second,
		IdleTimeout:   2 * time.Second,
		LingerTimeout: 50 * time.Millisecond,
		Handler:       func(ctx *rawhttp.Ctx) { ctx.SetBodyString(strings.Repeat("x", 1024)) },
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	addr := ln.Addr().String()

	// Warm up the listener goroutines before counting.
	if c, err := net.Dial("tcp", addr); err == nil {
		_, _ = io.WriteString(c, "GET / HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n")
		_, _ = io.Copy(io.Discard, c)
		_ = c.Close()
	}
	time.Sleep(100 * time.Millisecond)
	before := runtime.NumGoroutine()

	for i := 0; i < 200; i++ {
		c, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(c, "GET / HTTP/1.1\r\nHost: x\r\n\r\nGET / HTTP/1.1\r\nHost: x\r\n\r\n")
		br := bufio.NewReader(c)
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, _ = br.ReadString('\n')
		_ = c.Close()
	}
	// +2 goroutine slack can hide one ServeConn still finishing the last
	// response; wait for the counter itself before declaring a leak.
	openDeadline := time.Now().Add(5 * time.Second)
	for srv.OpenConnections.Load() != 0 && time.Now().Before(openDeadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if n := goroutinesSettle(before+2, 5*time.Second); n > before+2 {
		t.Fatalf("goroutines %d -> %d after 200 disconnects\n%s", before, n, dumpGoroutines())
	}
	if n := srv.OpenConnections.Load(); n != 0 {
		t.Fatalf("OpenConnections=%d\n%s", n, dumpGoroutines())
	}
}
