package rawhttp

import (
	"bytes"
	"io"
	"net"
	"runtime"
	"testing"
)

// The hijack callback is one closure per connection. Keep-alive requests
// assign that same func value after reset and must not allocate another.
func TestKeepAliveHijackMarkDoesNotAllocate(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{
		Handler: func(ctx *Ctx) {
			ctx.SetContentType("text/plain")
			ctx.SetBodyString("ok")
		},
		ReadTimeout:       -1,
		WriteTimeout:      -1,
		IdleTimeout:       -1,
		ReadHeaderTimeout: -1,
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	req := []byte("GET / HTTP/1.1\r\nHost: localhost\r\n\r\n")
	ack := make([]byte, 1024)
	for i := 0; i < 32; i++ {
		if _, err := conn.Write(req); err != nil {
			t.Fatal(err)
		}
		if err := readOneHTTP(conn, ack); err != nil {
			t.Fatal(err)
		}
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	const n = 200
	for i := 0; i < n; i++ {
		if _, err := conn.Write(req); err != nil {
			t.Fatal(err)
		}
		if err := readOneHTTP(conn, ack); err != nil {
			t.Fatal(err)
		}
	}
	runtime.ReadMemStats(&after)
	per := float64(after.Mallocs-before.Mallocs) / float64(n)
	if per > 0.1 {
		t.Fatalf("keep-alive alloc/req = %.3f, want ≤ 0.1", per)
	}
}

func readOneHTTP(c net.Conn, buf []byte) error {
	n := 0
	for {
		if n == len(buf) {
			return io.ErrShortBuffer
		}
		r, err := c.Read(buf[n:])
		n += r
		if err != nil {
			return err
		}
		if i := bytes.Index(buf[:n], []byte("\r\n\r\n")); i >= 0 {
			body := n - (i + 4)
			need := 0
			key := []byte("Content-Length: ")
			if j := bytes.Index(buf[:i], key); j >= 0 {
				for _, b := range buf[j+len(key) : i] {
					if b < '0' || b > '9' {
						break
					}
					need = need*10 + int(b-'0')
				}
			}
			for body < need {
				if n == len(buf) {
					return io.ErrShortBuffer
				}
				r, err = c.Read(buf[n:])
				n += r
				body += r
				if err != nil {
					return err
				}
			}
			return nil
		}
	}
}
