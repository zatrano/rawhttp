package rawhttp_test

import (
	"bufio"
	"io"
	"log"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/zatrano/rawhttp"
)

// Regression tests for findings recorded in the framework's docs/REVIEW-LOG.md.

// A StreamWriter runs on its own goroutine; its panic must not kill the
// process. The response is cut short and the connection is closed.
func TestStreamWriterPanicIsContained(t *testing.T) {
	served := 0
	srv := &rawhttp.Server{
		ErrorLog: log.New(io.Discard, "", 0),
		Handler: func(ctx *rawhttp.Ctx) {
			served++
			ctx.SetBodyStreamWriter(func(w *bufio.Writer) {
				_, _ = w.WriteString("partial")
				_ = w.Flush()
				panic("stream producer")
			})
		},
	}
	wire := serveRaw(t, srv, "GET /1 HTTP/1.1\r\nHost: x\r\n\r\nGET /2 HTTP/1.1\r\nHost: x\r\n\r\n")
	if served != 1 {
		t.Fatalf("served %d requests after a stream panic", served)
	}
	if strings.HasSuffix(wire, "0\r\n\r\n") {
		t.Fatalf("a panicked stream ended with a valid last chunk: %q", wire)
	}
}

type panicReader struct{}

func (panicReader) Read([]byte) (int, error) { panic("reader") }

// A handler-supplied body reader runs after the handler returned, outside its
// panic recovery.
func TestStreamBodyReaderPanicIsContained(t *testing.T) {
	for _, size := range []int{-1, 10} {
		srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) { ctx.SetBodyStream(panicReader{}, size) }}
		wire := serveRaw(t, srv, "GET / HTTP/1.1\r\nHost: x\r\n\r\nGET /2 HTTP/1.1\r\nHost: x\r\n\r\n")
		if strings.Count(wire, "HTTP/1.1 200") != 1 {
			t.Fatalf("size %d: %q", size, wire)
		}
	}
}

// A Content-Length claim does not allocate the whole body before it arrives.
func TestDeclaredBodyLengthIsNotAllocatedUpFront(t *testing.T) {
	srv := &rawhttp.Server{Handler: func(*rawhttp.Ctx) {}}
	raw := "POST / HTTP/1.1\r\nHost: x\r\nContent-Length: 4194304\r\n\r\n" + strings.Repeat("a", 1000)
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	for i := 0; i < 20; i++ {
		_ = srv.ServeConn(newFakeConn([]byte(raw)))
	}
	runtime.ReadMemStats(&after)
	perConn := (after.TotalAlloc - before.TotalAlloc) / 20
	if perConn > 1<<20 {
		t.Fatalf("a stalled 4 MiB claim allocated %d bytes per connection", perConn)
	}
}

func TestDeclaredBodyStillReadsInFull(t *testing.T) {
	body := strings.Repeat("0123456789", 30000) // 300 KB, above the up-front size
	var got string
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) { got = string(ctx.Body()) }}
	serveRaw(t, srv, "POST / HTTP/1.1\r\nHost: x\r\nConnection: close\r\nContent-Length: 300000\r\n\r\n"+body)
	if got != body {
		t.Fatalf("body len %d, want %d", len(got), len(body))
	}
}

func TestAllowedHostsCannotBeBypassedWithAPortSuffix(t *testing.T) {
	for _, host := range []string{"app.local:evil", "app.local:80:81", "app.local:?x", "app.local:/x"} {
		served := 0
		srv := &rawhttp.Server{AllowedHosts: []string{"app.local"}, Handler: func(*rawhttp.Ctx) { served++ }}
		wire := serveRaw(t, srv, "GET / HTTP/1.1\r\nHost: "+host+"\r\nConnection: close\r\n\r\n")
		if served != 0 || !strings.HasPrefix(wire, "HTTP/1.1 4") {
			t.Fatalf("Host %q: served=%d status=%q", host, served, firstLine(wire))
		}
	}
	for _, host := range []string{"app.local", "app.local:8080", "APP.local."} {
		served := 0
		srv := &rawhttp.Server{AllowedHosts: []string{"app.local"}, Handler: func(*rawhttp.Ctx) { served++ }}
		serveRaw(t, srv, "GET / HTTP/1.1\r\nHost: "+host+"\r\nConnection: close\r\n\r\n")
		if served != 1 {
			t.Fatalf("Host %q rejected", host)
		}
	}
	served := 0
	srv := &rawhttp.Server{AllowedHosts: []string{"[::1]"}, Handler: func(*rawhttp.Ctx) { served++ }}
	serveRaw(t, srv, "GET / HTTP/1.1\r\nHost: [::1]:8080\r\nConnection: close\r\n\r\n")
	if served != 1 {
		t.Fatal("bracketed IPv6 host with a port did not match its allowlist entry")
	}
}

// After a handler panic with a streamed request body, the engine does not
// wait for the rest of the body before answering.
func TestStreamedBodyPanicDoesNotDrain(t *testing.T) {
	srv := &rawhttp.Server{
		StreamRequestBody: true,
		ReadTimeout:       5 * time.Second,
		ErrorLog:          log.New(io.Discard, "", 0),
		Handler:           func(*rawhttp.Ctx) { panic("handler") },
	}
	conn := &stallConn{fakeConn: newFakeConn([]byte("POST / HTTP/1.1\r\nHost: x\r\nContent-Length: 1000000\r\n\r\nabc"))}
	done := make(chan struct{})
	start := time.Now()
	go func() { _ = srv.ServeConn(conn); close(done) }()
	select {
	case <-done:
	case <-time.After(4 * time.Second):
		t.Fatal("ServeConn waited for the unread body after a panic")
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("ServeConn waited for the read deadline")
	}
	conn.fakeConn.mu.Lock()
	wire := conn.fakeConn.w.String()
	conn.fakeConn.mu.Unlock()
	if !strings.HasPrefix(wire, "HTTP/1.1 500") || !strings.Contains(wire, "Connection: close") {
		t.Fatalf("want a closing 500, got %q", wire)
	}
}

func TestChunkSizeMaxIntDoesNotBypassBodyLimit(t *testing.T) {
	raw := "POST / HTTP/1.1\r\nHost: x\r\nTransfer-Encoding: chunked\r\n\r\n7fffffffffffffff\r\n"
	run := runStates(t, &rawhttp.Server{}, raw)
	if len(run.paths) != 0 || !strings.HasPrefix(run.wire, "HTTP/1.1 413") {
		t.Fatalf("overflow chunk size: paths=%v status=%q", run.paths, firstLine(run.wire))
	}
}

func TestKeepAliveBodyCacheDoesNotReplayPreviousBody(t *testing.T) {
	var slot [5]byte
	srv := &rawhttp.Server{
		Handler: func(ctx *rawhttp.Ctx) {
			p := ctx.Path
			if len(p) >= 6 {
				copy(slot[:], p[1:6])
			}
			ctx.SetBody(slot[:])
		},
	}
	wire := serveRaw(t, srv, "GET /aaaaa HTTP/1.1\r\nHost: x\r\n\r\nGET /bbbbb HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n")
	if strings.Count(wire, "aaaaa") != 1 || strings.Count(wire, "bbbbb") != 1 {
		t.Fatalf("keep-alive body cache replayed the first response: %q", wire)
	}
}
