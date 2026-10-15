package bench_test

import (
	"bytes"
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

func newDiscardConn(data []byte) *fakeConn {
	return &fakeConn{r: bytes.NewReader(data), discard: true, doneCh: make(chan struct{})}
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
