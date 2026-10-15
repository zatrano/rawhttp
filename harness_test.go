package rawhttp_test

import (
	"bytes"
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/zatrano/rawhttp"
)

func skipIfPoison(t testing.TB) {
	t.Helper()
	if rawhttp.PoisonBuildEnabled() {
		t.Skip("rawhttp_poison: skip gates/benches (0xDE fill skews ns/op and alloc contracts)")
	}
}

type fakeConn struct {
	r             *bytes.Reader
	w             bytes.Buffer
	discard       bool
	doneCh        chan struct{}
	once          sync.Once
	mu            sync.Mutex
	readDeadline  time.Time
	writeDeadline time.Time
}

func newFakeConn(data []byte) *fakeConn {
	return &fakeConn{r: bytes.NewReader(data), doneCh: make(chan struct{})}
}

func newDiscardConn(data []byte) *fakeConn {
	c := newFakeConn(data)
	c.discard = true
	return c
}

func (c *fakeConn) deadlineErr(d time.Time) error {
	if !d.IsZero() && !time.Now().Before(d) {
		return os.ErrDeadlineExceeded
	}
	return nil
}

func (c *fakeConn) Read(b []byte) (int, error) {
	c.mu.Lock()
	d := c.readDeadline
	c.mu.Unlock()
	if err := c.deadlineErr(d); err != nil {
		return 0, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.r.Read(b)
}
func (c *fakeConn) Write(b []byte) (int, error) {
	c.mu.Lock()
	d := c.writeDeadline
	c.mu.Unlock()
	if err := c.deadlineErr(d); err != nil {
		return 0, err
	}
	if c.discard {
		return len(b), nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.w.Write(b)
}
func (c *fakeConn) Close() error {
	c.once.Do(func() { close(c.doneCh) })
	return nil
}
func (c *fakeConn) LocalAddr() net.Addr  { return dummyAddr{} }
func (c *fakeConn) RemoteAddr() net.Addr { return dummyAddr{} }
func (c *fakeConn) SetDeadline(t time.Time) error {
	c.mu.Lock()
	c.readDeadline = t
	c.writeDeadline = t
	c.mu.Unlock()
	return nil
}
func (c *fakeConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	c.readDeadline = t
	c.mu.Unlock()
	return nil
}
func (c *fakeConn) SetWriteDeadline(t time.Time) error {
	c.mu.Lock()
	c.writeDeadline = t
	c.mu.Unlock()
	return nil
}

type dummyAddr struct{}

func (dummyAddr) Network() string { return "fake" }
func (dummyAddr) String() string  { return "127.0.0.1:12345" }

var plaintextBody = []byte("Hello, World!")

const oneRequest = "GET / HTTP/1.1\r\nHost: localhost\r\nUser-Agent: bench\r\nAccept: */*\r\n\r\n"

func buildRequests(n int) []byte {
	var b bytes.Buffer
	b.Grow(len(oneRequest) * n)
	for i := 0; i < n; i++ {
		b.WriteString(oneRequest)
	}
	return b.Bytes()
}

type oneConnListener struct {
	conn net.Conn
	used bool
}

var errListenerDone = errors.New("benchmark: listener exhausted")

func (l *oneConnListener) Accept() (net.Conn, error) {
	if l.used {
		return nil, errListenerDone
	}
	l.used = true
	return l.conn, nil
}
func (l *oneConnListener) Close() error   { return nil }
func (l *oneConnListener) Addr() net.Addr { return dummyAddr{} }

type chunkedConn struct {
	data      []byte
	pos       int
	chunkSize int
	w         bytes.Buffer
}

func newChunkedConn(data []byte, chunkSize int) *chunkedConn {
	return &chunkedConn{data: data, chunkSize: chunkSize}
}

func (c *chunkedConn) Read(b []byte) (int, error) {
	if c.pos >= len(c.data) {
		return 0, io.EOF
	}
	n := c.chunkSize
	if n > len(b) {
		n = len(b)
	}
	remaining := len(c.data) - c.pos
	if n > remaining {
		n = remaining
	}
	copy(b, c.data[c.pos:c.pos+n])
	c.pos += n
	return n, nil
}
func (c *chunkedConn) Write(b []byte) (int, error)      { return c.w.Write(b) }
func (c *chunkedConn) Close() error                     { return nil }
func (c *chunkedConn) LocalAddr() net.Addr              { return dummyAddr{} }
func (c *chunkedConn) RemoteAddr() net.Addr             { return dummyAddr{} }
func (c *chunkedConn) SetDeadline(time.Time) error      { return nil }
func (c *chunkedConn) SetReadDeadline(time.Time) error  { return nil }
func (c *chunkedConn) SetWriteDeadline(time.Time) error { return nil }
