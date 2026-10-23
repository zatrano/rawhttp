package rawhttp_test

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zatrano/rawhttp"
)

// R3: every default limit holds with a zero-value Server.

func TestDefaultLimitHeaderBytes(t *testing.T) {
	big := strings.Repeat("a", 9000)
	run := runStates(t, &rawhttp.Server{}, "GET / HTTP/1.1\r\nHost: x\r\nX-Big: "+big+"\r\n\r\n")
	if len(run.paths) != 0 || !strings.HasPrefix(run.wire, "HTTP/1.1 431") {
		t.Fatalf("paths=%v status=%q", run.paths, firstLine(run.wire))
	}
}

func TestDefaultLimitHeaderCount(t *testing.T) {
	var b strings.Builder
	b.WriteString("GET / HTTP/1.1\r\nHost: x\r\n")
	for i := 0; i < 101; i++ {
		b.WriteString("X-H" + strconv.Itoa(i) + ": v\r\n")
	}
	b.WriteString("\r\n")
	run := runStates(t, &rawhttp.Server{}, b.String())
	if len(run.paths) != 0 || !strings.HasPrefix(run.wire, "HTTP/1.1 4") {
		t.Fatalf("101 headers: paths=%v status=%q", run.paths, firstLine(run.wire))
	}
}

func TestDefaultLimitBodySize(t *testing.T) {
	size := 4<<20 + 1
	raw := "POST / HTTP/1.1\r\nHost: x\r\nContent-Length: " + strconv.Itoa(size) + "\r\n\r\n" + strings.Repeat("a", size)
	run := runStates(t, &rawhttp.Server{}, raw)
	if len(run.paths) != 0 || !strings.HasPrefix(run.wire, "HTTP/1.1 413") {
		t.Fatalf("paths=%v status=%q", run.paths, firstLine(run.wire))
	}
}

func TestDefaultLimitChunkedBodySize(t *testing.T) {
	var b strings.Builder
	b.WriteString("POST / HTTP/1.1\r\nHost: x\r\nTransfer-Encoding: chunked\r\n\r\n")
	chunk := strings.Repeat("a", 64<<10)
	for i := 0; i < 65; i++ {
		b.WriteString("10000\r\n" + chunk + "\r\n")
	}
	b.WriteString("0\r\n\r\n")
	run := runStates(t, &rawhttp.Server{}, b.String())
	if len(run.paths) != 0 || !strings.HasPrefix(run.wire, "HTTP/1.1 413") {
		t.Fatalf("paths=%v status=%q", run.paths, firstLine(run.wire))
	}
}

func TestDefaultLimitChunkCount(t *testing.T) {
	var b strings.Builder
	b.WriteString("POST / HTTP/1.1\r\nHost: x\r\nTransfer-Encoding: chunked\r\n\r\n")
	for i := 0; i < 16385; i++ {
		b.WriteString("1\r\na\r\n")
	}
	b.WriteString("0\r\n\r\n")
	run := runStates(t, &rawhttp.Server{}, b.String())
	if len(run.paths) != 0 || !strings.HasPrefix(run.wire, "HTTP/1.1 4") {
		t.Fatalf("16385 chunks: paths=%v status=%q", run.paths, firstLine(run.wire))
	}
}

// The request line lives in the header block, so MaxHeaderBytes bounds it
// even when MaxURILength is zero.
func TestDefaultLimitRequestLine(t *testing.T) {
	run := runStates(t, &rawhttp.Server{}, "GET /"+strings.Repeat("a", 9000)+" HTTP/1.1\r\nHost: x\r\n\r\n")
	if len(run.paths) != 0 || !strings.HasPrefix(run.wire, "HTTP/1.1 4") {
		t.Fatalf("paths=%d status=%q", len(run.paths), firstLine(run.wire))
	}
}

func TestDefaultLimitChunkSizeOverflow(t *testing.T) {
	raw := "POST / HTTP/1.1\r\nHost: x\r\nTransfer-Encoding: chunked\r\n\r\nffffffffffffffffff\r\n"
	run := runStates(t, &rawhttp.Server{}, raw)
	if len(run.paths) != 0 || !strings.HasPrefix(run.wire, "HTTP/1.1 4") {
		t.Fatalf("paths=%v status=%q", run.paths, firstLine(run.wire))
	}
}

func TestDefaultLimitContentLengthOverflow(t *testing.T) {
	raw := "POST / HTTP/1.1\r\nHost: x\r\nContent-Length: 99999999999999999999999\r\n\r\n"
	run := runStates(t, &rawhttp.Server{}, raw)
	if len(run.paths) != 0 || !strings.HasPrefix(run.wire, "HTTP/1.1 4") {
		t.Fatalf("paths=%v status=%q", run.paths, firstLine(run.wire))
	}
}

// A partial request that never completes ends when the read deadline fires.
func TestReadTimeoutEndsStalledRequest(t *testing.T) {
	srv := &rawhttp.Server{ReadTimeout: 50 * time.Millisecond, Handler: func(*rawhttp.Ctx) {}}
	conn := &stallConn{fakeConn: newFakeConn([]byte("GET / HTTP/1.1\r\nHost: x\r\n"))}
	done := make(chan error, 1)
	go func() { done <- srv.ServeConn(conn) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ServeConn did not return after the read deadline")
	}
}

// stallConn returns the scripted bytes, then blocks until the read deadline.
type stallConn struct{ *fakeConn }

func (c *stallConn) Read(b []byte) (int, error) {
	n, err := c.fakeConn.Read(b)
	if n > 0 || err == nil {
		return n, err
	}
	for {
		c.fakeConn.mu.Lock()
		d := c.fakeConn.readDeadline
		c.fakeConn.mu.Unlock()
		if err := c.fakeConn.deadlineErr(d); err != nil {
			return 0, err
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func firstLine(s string) string {
	if i := strings.Index(s, "\r\n"); i >= 0 {
		return s[:i]
	}
	return s
}

func TestMaxPipelineDepthCloses(t *testing.T) {
	raw := strings.Repeat("GET /x HTTP/1.1\r\nHost: x\r\n\r\n", 4)
	run := runStates(t, &rawhttp.Server{MaxPipelineDepth: 2}, raw)
	if len(run.paths) != 2 {
		t.Fatalf("served %d with MaxPipelineDepth=2: %v", len(run.paths), run.paths)
	}
}

func TestDefaultPipelineDepthAllowsShortPipeline(t *testing.T) {
	raw := strings.Repeat("GET /x HTTP/1.1\r\nHost: x\r\n\r\n", 8)
	run := runStates(t, &rawhttp.Server{}, raw)
	if len(run.paths) != 8 {
		t.Fatalf("default pipeline depth served %d of 8: %v", len(run.paths), run.paths)
	}
}

func TestDefaultMaxRequestsPerConnStillKeepsAlive(t *testing.T) {
	raw := "GET /a HTTP/1.1\r\nHost: x\r\n\r\nGET /b HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n"
	run := runStates(t, &rawhttp.Server{}, raw)
	if strings.Join(run.paths, ",") != "/a,/b" {
		t.Fatalf("paths=%v", run.paths)
	}
}
