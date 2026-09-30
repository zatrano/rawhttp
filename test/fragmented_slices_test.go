package test_test

import (
	"bytes"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zatrano/rawhttp"
)

// Fragmented / keep-alive slice lifetime:
// - Within a single handler invocation, Method/Path/Host/header slices must stay
//   stable even when the request arrives via small TCP reads.
// - Retaining those slices after the handler returns is unsupported; a later
//   keep-alive request on the same connection may overwrite the underlying buffer.

type fragmentConn struct {
	r      *bytes.Reader
	w      bytes.Buffer
	sz     int
	mu     sync.Mutex
	closed bool
}

func newFragmentConn(data []byte, chunk int) *fragmentConn {
	if chunk < 1 {
		chunk = 1
	}
	return &fragmentConn{r: bytes.NewReader(data), sz: chunk}
}

func (c *fragmentConn) Read(b []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return 0, io.EOF
	}
	n := c.sz
	if n > len(b) {
		n = len(b)
	}
	tmp := make([]byte, n)
	rn, err := c.r.Read(tmp)
	copy(b, tmp[:rn])
	return rn, err
}
func (c *fragmentConn) Write(b []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.w.Write(b)
}
func (c *fragmentConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}
func (c *fragmentConn) LocalAddr() net.Addr              { return dummyAddr{} }
func (c *fragmentConn) RemoteAddr() net.Addr             { return dummyAddr{} }
func (c *fragmentConn) SetDeadline(time.Time) error      { return nil }
func (c *fragmentConn) SetReadDeadline(time.Time) error  { return nil }
func (c *fragmentConn) SetWriteDeadline(time.Time) error { return nil }

func TestFragmentedSlicesStableInsideHandler(t *testing.T) {
	const req1 = "GET /one HTTP/1.1\r\nHost: a.example\r\nX-Trace: alpha\r\n\r\n"
	const req2 = "GET /two HTTP/1.1\r\nHost: b.example\r\nX-Trace: bravo\r\n\r\n"
	payload := []byte(req1 + req2)

	var saw []string
	srv := &rawhttp.Server{
		ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
		Handler: func(ctx *rawhttp.Ctx) {
			m, p, h, tr := ctx.Method, ctx.Path, ctx.Host(), ctx.Header("X-Trace")
			em, ep, eh, et := string(m), string(p), string(h), string(tr)
			time.Sleep(1 * time.Millisecond)
			if string(m) != em || string(p) != ep || string(h) != eh || string(tr) != et {
				t.Errorf("slice mutated inside handler: got %q %q %q %q want %q %q %q %q",
					m, p, h, tr, em, ep, eh, et)
			}
			saw = append(saw, ep+"|"+eh+"|"+et)
			ctx.SetBody([]byte("ok"))
		},
	}
	conn := newFragmentConn(payload, 3)
	_ = srv.ServeConn(conn)
	if len(saw) != 2 {
		t.Fatalf("expected 2 handler calls, got %d (%v); resp=%q", len(saw), saw, conn.w.String())
	}
	if saw[0] != "/one|a.example|alpha" || saw[1] != "/two|b.example|bravo" {
		t.Fatalf("unexpected observations: %v", saw)
	}
}

func TestRetainedSliceLifetimeReuse(t *testing.T) {
	const req1 = "GET /first HTTP/1.1\r\nHost: h1.example\r\nX-Id: one\r\n\r\n"
	const req2 = "GET /second HTTP/1.1\r\nHost: h2.example\r\nX-Id: two\r\n\r\n"

	var retainedMethod, retainedPath, retainedHost, retainedHdr []byte
	var firstCopyM, firstCopyP, firstCopyH, firstCopyHdr string
	n := 0
	srv := &rawhttp.Server{
		ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
		Handler: func(ctx *rawhttp.Ctx) {
			n++
			if n == 1 {
				retainedMethod = ctx.Method
				retainedPath = ctx.Path
				retainedHost = ctx.Host()
				retainedHdr = ctx.Header("X-Id")
				firstCopyM = string(ctx.Method)
				firstCopyP = string(ctx.Path)
				firstCopyH = string(ctx.Host())
				firstCopyHdr = string(ctx.Header("X-Id"))
			}
			ctx.SetBody([]byte("ok"))
		},
	}
	conn := newFakeConn([]byte(req1 + req2))
	_ = srv.ServeConn(conn)
	if n != 2 {
		t.Fatalf("want 2 requests, got %d", n)
	}
	stillSame := string(retainedMethod) == firstCopyM &&
		string(retainedPath) == firstCopyP &&
		string(retainedHost) == firstCopyH &&
		string(retainedHdr) == firstCopyHdr
	t.Logf("retained after req2: method=%q path=%q host=%q xid=%q (first %q %q %q %q) same=%v",
		retainedMethod, retainedPath, retainedHost, retainedHdr,
		firstCopyM, firstCopyP, firstCopyH, firstCopyHdr, stillSame)
	if stillSame {
		t.Log("NOTE: retained slices still matched on this run; retention remains unsupported by docs")
	} else {
		t.Log("OBSERVED: retained slices diverged after subsequent request")
	}
}

func TestRetainedSliceOverwrittenByLargeSecondRequest(t *testing.T) {
	pad := strings.Repeat("Z", 6000)
	req1 := "GET /first HTTP/1.1\r\nHost: h1.example\r\nX-Id: one\r\n\r\n"
	req2 := "GET /second HTTP/1.1\r\nHost: h2.example\r\nX-Pad: " + pad + "\r\nX-Id: two\r\n\r\n"

	var retainedPath []byte
	var firstCopy string
	n := 0
	srv := &rawhttp.Server{
		ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
		ReadBufferSize: 8192,
		Handler: func(ctx *rawhttp.Ctx) {
			n++
			if n == 1 {
				retainedPath = ctx.Path
				firstCopy = string(ctx.Path)
			}
			ctx.SetBody([]byte("ok"))
		},
	}
	conn := newFakeConn([]byte(req1 + req2))
	_ = srv.ServeConn(conn)
	if n != 2 {
		t.Fatalf("want 2 requests, got %d", n)
	}
	same := string(retainedPath) == firstCopy
	t.Logf("retained path after large req2: %q (first %q) same=%v", retainedPath, firstCopy, same)
	if same {
		t.Log("NOTE: path bytes not overwritten this run; retention still unsupported")
	} else {
		t.Log("OBSERVED: retained Path bytes changed after subsequent large request")
	}
}
