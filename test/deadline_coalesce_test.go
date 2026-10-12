package test_test

import (
	"bufio"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zatrano/rawhttp"
)

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
