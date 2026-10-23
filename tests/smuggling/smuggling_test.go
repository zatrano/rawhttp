package smuggling_test

import (
	"bytes"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zatrano/rawhttp"
	"github.com/zatrano/rawhttp/tests/smuggling"
)

// memConn serves a fixed byte stream and records what the server writes.
type memConn struct {
	mu sync.Mutex
	r  *bytes.Reader
	w  bytes.Buffer
}

func (c *memConn) Read(b []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.r.Read(b)
}

func (c *memConn) Write(b []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.w.Write(b)
}

func (c *memConn) Close() error                     { return nil }
func (c *memConn) LocalAddr() net.Addr              { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 80} }
func (c *memConn) RemoteAddr() net.Addr             { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 50000} }
func (c *memConn) SetDeadline(time.Time) error      { return nil }
func (c *memConn) SetReadDeadline(time.Time) error  { return nil }
func (c *memConn) SetWriteDeadline(time.Time) error { return nil }

type served struct {
	method, path string
	body         string
}

func serve(t *testing.T, raw string) (got []served, wire string) {
	t.Helper()
	var mu sync.Mutex
	srv := &rawhttp.Server{
		ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
		LingerTimeout: time.Millisecond,
		Handler: func(ctx *rawhttp.Ctx) {
			mu.Lock()
			got = append(got, served{method: string(ctx.Method), path: string(ctx.Path), body: string(ctx.Body())})
			mu.Unlock()
			ctx.SetBodyString("ok")
		},
	}
	c := &memConn{r: bytes.NewReader([]byte(raw))}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.ServeConn(c)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("ServeConn did not return")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return got, c.w.String()
}

func responseCount(wire string) int {
	// Keep-alive responses are concatenated: the next status line starts
	// immediately after the body, so "HTTP/1.1 " is not always at the
	// start of a CRLF-delimited line (e.g. "okHTTP/1.1 200 OK").
	return strings.Count(wire, "HTTP/1.1 ") + strings.Count(wire, "HTTP/1.0 ")
}

// TestSmugglingCorpus checks invariants R1 and R4: ambiguous framing is
// rejected, nothing on the connection is served after a parse error, and a
// valid stream yields exactly the requests its framing describes.
func TestSmugglingCorpus(t *testing.T) {
	for _, tc := range smuggling.Cases() {
		t.Run(tc.Name, func(t *testing.T) {
			if tc.Reject {
				got, wire := serve(t, tc.Raw+smuggling.Follow)
				if len(got) != 0 {
					t.Fatalf("%s (%s): handler ran %d time(s): %+v", tc.Name, tc.Ref, len(got), got)
				}
				if !strings.HasPrefix(wire, "HTTP/1.1 4") {
					t.Fatalf("%s: want a 4xx, wire=%q", tc.Name, firstLine(wire))
				}
				if !strings.Contains(headerBlock(wire), "Connection: close") {
					t.Fatalf("%s: rejection does not close the connection: %q", tc.Name, headerBlock(wire))
				}
				if n := responseCount(wire); n != 1 {
					t.Fatalf("%s: %d responses after a parse error, want 1: %q", tc.Name, n, wire)
				}
				return
			}
			got, wire := serve(t, tc.Raw)
			if len(got) != len(tc.Bodies) {
				t.Fatalf("%s (%s): served %d request(s), want %d: %+v wire=%q", tc.Name, tc.Ref, len(got), len(tc.Bodies), got, wire)
			}
			for i, body := range tc.Bodies {
				if got[i].body != body {
					t.Fatalf("%s: request %d body %q, want %q", tc.Name, i, got[i].body, body)
				}
			}
			if n := responseCount(wire); n != len(tc.Bodies) {
				t.Fatalf("%s: %d responses, want %d", tc.Name, n, len(tc.Bodies))
			}
		})
	}
}

// TestSmugglingCorpusSplitReads replays every case one byte per Read so the
// parser sees each boundary at a buffer edge.
func TestSmugglingCorpusSplitReads(t *testing.T) {
	for _, tc := range smuggling.Cases() {
		t.Run(tc.Name, func(t *testing.T) {
			raw := tc.Raw
			if tc.Reject {
				raw += smuggling.Follow
			}
			var handled int
			srv := &rawhttp.Server{
				ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
				LingerTimeout: time.Millisecond,
				Handler: func(ctx *rawhttp.Ctx) {
					handled++
					ctx.SetBodyString("ok")
				},
			}
			c := &trickleConn{data: []byte(raw)}
			_ = srv.ServeConn(c)
			want := len(tc.Bodies)
			if tc.Reject {
				want = 0
			}
			if handled != want {
				t.Fatalf("%s: handled %d with one-byte reads, want %d", tc.Name, handled, want)
			}
		})
	}
}

type trickleConn struct {
	memConn
	data []byte
	pos  int
}

func (c *trickleConn) Read(b []byte) (int, error) {
	if c.pos >= len(c.data) {
		return 0, io.EOF
	}
	b[0] = c.data[c.pos]
	c.pos++
	return 1, nil
}

func (c *trickleConn) Write(b []byte) (int, error) { return c.memConn.w.Write(b) }

func firstLine(s string) string {
	if i := strings.Index(s, "\r\n"); i >= 0 {
		return s[:i]
	}
	return s
}

func headerBlock(s string) string {
	if i := strings.Index(s, "\r\n\r\n"); i >= 0 {
		return s[:i]
	}
	return s
}
