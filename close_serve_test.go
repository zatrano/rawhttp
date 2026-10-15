package rawhttp_test

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/zatrano/rawhttp"
)

func TestCloseDuringServeWaitsForAdmit(t *testing.T) {
	const rounds = 20
	body := "GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
	for i := 0; i < rounds; i++ {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		srv := &rawhttp.Server{
			ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
			Handler: func(*rawhttp.Ctx) {},
		}
		errc := make(chan error, 1)
		go func() { errc <- srv.Serve(ln) }()

		conn, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			_ = srv.Close()
			t.Fatal(err)
		}
		if _, err := io.WriteString(conn, body); err != nil {
			_ = conn.Close()
			_ = srv.Close()
			t.Fatal(err)
		}
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, _ = io.Copy(io.Discard, conn)
		_ = conn.Close()

		if err := srv.Close(); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-errc:
			if err != nil && err != rawhttp.ErrServerClosed {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Serve did not return after Close")
		}
	}
}
