package test_test

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/zatrano/rawhttp"
)

// Slow-header / slow-body clients must be cut by ReadHeaderTimeout / ReadTimeout
// before the handler runs to completion.

func TestSlowHeaderHitsReadHeaderTimeout(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	called := false
	srv := &rawhttp.Server{
		ReadHeaderTimeout: 200 * time.Millisecond,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      5 * time.Second,
		IdleTimeout:       5 * time.Second,
		Handler:           func(ctx *rawhttp.Ctx) { called = true },
	}
	go srv.Serve(ln) //nolint:errcheck
	defer srv.Close()

	conn, err := net.DialTimeout("tcp", ln.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	msg := []byte("GET / HTTP/1.1\r\nHost: localhost\r\nX-Slow: abcdefghijklmnopqrstuvwxyz\r\n\r\n")
	for i := 0; i < len(msg); i++ {
		if _, err := conn.Write(msg[i : i+1]); err != nil {
			// Connection closed by server mid-drip is success for this test.
			t.Logf("write stopped at byte %d: %v", i, err)
			break
		}
		time.Sleep(80 * time.Millisecond)
	}
	time.Sleep(400 * time.Millisecond)
	_, _ = io.Copy(io.Discard, conn)
	if called {
		t.Fatal("handler must not run when headers drip past ReadHeaderTimeout")
	}
}

func TestSlowBodyHitsReadTimeout(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	called := false
	srv := &rawhttp.Server{
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       200 * time.Millisecond,
		WriteTimeout:      2 * time.Second,
		IdleTimeout:       2 * time.Second,
		Handler:           func(ctx *rawhttp.Ctx) { called = true; _ = ctx.Body() },
	}
	go srv.Serve(ln) //nolint:errcheck
	defer srv.Close()

	conn, err := net.DialTimeout("tcp", ln.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	hdr := "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 20\r\n\r\n"
	if _, err := io.WriteString(conn, hdr); err != nil {
		t.Fatal(err)
	}
	// Drip body slower than ReadTimeout.
	for i := 0; i < 20; i++ {
		if _, err := conn.Write([]byte("x")); err != nil {
			t.Logf("body write stopped at %d: %v", i, err)
			break
		}
		time.Sleep(80 * time.Millisecond)
	}
	time.Sleep(400 * time.Millisecond)
	if called {
		t.Fatal("handler must not complete successfully on slow body past ReadTimeout")
	}
}
