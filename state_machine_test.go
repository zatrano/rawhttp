package rawhttp_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/zatrano/rawhttp"
)

// The connection state machine: Idle -> ReadingHeaders -> ReadingBody ->
// Handler -> Writing -> (Idle | Closed). These tests pin every transition
// that decides whether bytes after a request are parsed as a new request.

type stateRun struct {
	paths []string
	wire  string
}

func runStates(t *testing.T, srv *rawhttp.Server, raw string) stateRun {
	t.Helper()
	var run stateRun
	inner := srv.Handler
	srv.Handler = func(ctx *rawhttp.Ctx) {
		run.paths = append(run.paths, string(ctx.Path))
		if inner != nil {
			inner(ctx)
			return
		}
		ctx.SetBodyString("ok")
	}
	run.wire = serveRaw(t, srv, raw)
	return run
}

func statusLines(wire string) []string {
	var out []string
	for remaining := wire; ; {
		i := strings.Index(remaining, "HTTP/1.")
		if i < 0 {
			return out
		}
		rest := remaining[i:]
		end := strings.Index(rest, "\r\n")
		if end < 0 {
			end = len(rest)
		}
		line := rest[:end]
		if len(line) >= 12 {
			out = append(out, line[:12])
		}
		if end >= len(rest) {
			return out
		}
		remaining = rest[end+2:]
	}
}

func TestStateKeepAliveServesInOrder(t *testing.T) {
	run := runStates(t, &rawhttp.Server{}, "GET /a HTTP/1.1\r\nHost: x\r\n\r\nGET /b HTTP/1.1\r\nHost: x\r\n\r\nGET /c HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n")
	if strings.Join(run.paths, ",") != "/a,/b,/c" || len(readAllResponses(t, run.wire)) != 3 {
		t.Fatalf("paths=%v wire=%q", run.paths, run.wire)
	}
}

func TestStateHTTP10ClosesByDefault(t *testing.T) {
	run := runStates(t, &rawhttp.Server{}, "GET /a HTTP/1.0\r\n\r\nGET /b HTTP/1.0\r\n\r\n")
	if len(run.paths) != 1 {
		t.Fatalf("HTTP/1.0 without keep-alive served %v", run.paths)
	}
}

func TestStateHTTP10KeepAlive(t *testing.T) {
	run := runStates(t, &rawhttp.Server{}, "GET /a HTTP/1.0\r\nConnection: keep-alive\r\n\r\nGET /b HTTP/1.0\r\n\r\n")
	if strings.Join(run.paths, ",") != "/a,/b" {
		t.Fatalf("paths=%v", run.paths)
	}
}

func TestStateClientConnectionCloseStops(t *testing.T) {
	run := runStates(t, &rawhttp.Server{}, "GET /a HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\nGET /b HTTP/1.1\r\nHost: x\r\n\r\n")
	if len(run.paths) != 1 {
		t.Fatalf("served after Connection: close: %v", run.paths)
	}
}

// R4: after a parse error nothing else on the connection is served.
func TestStateParseErrorEndsConnection(t *testing.T) {
	bad := []string{
		"GET /bad HTTP/1.1\r\nHost: x\r\nContent-Length: 1\r\nContent-Length: 2\r\n\r\nab",
		"GET /bad HTTP/1.1\nHost: x\n\n",
		"POST /bad HTTP/1.1\r\nHost: x\r\nTransfer-Encoding: chunked\r\n\r\nzz\r\n",
		"GET /bad HTTP/1.1\r\nHost: x\r\n bad-fold: y\r\n\r\n",
		"GET  /bad HTTP/1.1\r\nHost: x\r\n\r\n",
	}
	for _, b := range bad {
		raw := "GET /first HTTP/1.1\r\nHost: x\r\n\r\n" + b + "GET /after HTTP/1.1\r\nHost: x\r\n\r\n"
		run := runStates(t, &rawhttp.Server{}, raw)
		if strings.Join(run.paths, ",") != "/first" {
			t.Fatalf("%q: served %v", b, run.paths)
		}
		lines := statusLines(run.wire)
		if len(lines) != 2 || lines[0] != "HTTP/1.1 200" || !strings.HasPrefix(lines[1], "HTTP/1.1 4") {
			t.Fatalf("%q: status lines %v", b, lines)
		}
	}
}

// An unread Content-Length body is consumed by the engine, so its bytes are
// never parsed as the next request.
func TestStateUnreadBodyIsNotARequest(t *testing.T) {
	hidden := "GET /hidden HTTP/1.1\r\nHost: x\r\n\r\n"
	raw := "POST /a HTTP/1.1\r\nHost: x\r\nContent-Length: " + strconv.Itoa(len(hidden)) + "\r\n\r\n" + hidden +
		"GET /b HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n"
	run := runStates(t, &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) { ctx.SetBodyString("ignored body") }}, raw)
	if strings.Join(run.paths, ",") != "/a,/b" {
		t.Fatalf("paths=%v", run.paths)
	}
}

func TestStateUnreadStreamedBodyIsNotARequest(t *testing.T) {
	hidden := "GET /hidden HTTP/1.1\r\nHost: x\r\n\r\n"
	raw := "POST /a HTTP/1.1\r\nHost: x\r\nTransfer-Encoding: chunked\r\n\r\n" +
		strconv.FormatInt(int64(len(hidden)), 16) + "\r\n" + hidden + "\r\n0\r\n\r\n" +
		"GET /b HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n"
	run := runStates(t, &rawhttp.Server{StreamRequestBody: true}, raw)
	for _, p := range run.paths {
		if p == "/hidden" {
			t.Fatalf("body bytes were served as a request: %v", run.paths)
		}
	}
}

func TestStateMaxRequestsPerConn(t *testing.T) {
	run := runStates(t, &rawhttp.Server{MaxRequestsPerConn: 2}, strings.Repeat("GET /x HTTP/1.1\r\nHost: x\r\n\r\n", 4))
	if len(run.paths) != 2 {
		t.Fatalf("served %d with MaxRequestsPerConn=2", len(run.paths))
	}
	resps := readAllResponses(t, run.wire)
	if !resps[len(resps)-1].Close {
		t.Fatal("last response before the cap does not announce close")
	}
}

func TestStateDisablePipelining(t *testing.T) {
	run := runStates(t, &rawhttp.Server{DisablePipelining: true}, "GET /a HTTP/1.1\r\nHost: x\r\n\r\nGET /b HTTP/1.1\r\nHost: x\r\n\r\n")
	if len(run.paths) != 1 {
		t.Fatalf("pipelined request served with DisablePipelining: %v", run.paths)
	}
}

func TestStateUpgradeWithoutHijackCloses(t *testing.T) {
	raw := "GET /ws HTTP/1.1\r\nHost: x\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n" +
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n" +
		"GET /after HTTP/1.1\r\nHost: x\r\n\r\n"
	run := runStates(t, &rawhttp.Server{AllowUpgrade: true}, raw)
	if len(run.paths) != 1 {
		t.Fatalf("bytes after an un-hijacked upgrade were served: %v", run.paths)
	}
}

func TestStateUpgradeRejectedByDefault(t *testing.T) {
	raw := "GET /ws HTTP/1.1\r\nHost: x\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n" +
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n"
	run := runStates(t, &rawhttp.Server{}, raw)
	if len(run.paths) != 0 || !strings.HasPrefix(run.wire, "HTTP/1.1 400") {
		t.Fatalf("paths=%v wire=%q", run.paths, run.wire)
	}
}

func TestStateExpectContinueRejected(t *testing.T) {
	srv := &rawhttp.Server{ContinueHandler: func(*rawhttp.Ctx) bool { return false }}
	raw := "POST /a HTTP/1.1\r\nHost: x\r\nContent-Length: 3\r\nExpect: 100-continue\r\n\r\nabcGET /b HTTP/1.1\r\nHost: x\r\n\r\n"
	run := runStates(t, srv, raw)
	if len(run.paths) != 0 || !strings.HasPrefix(run.wire, "HTTP/1.1 417") {
		t.Fatalf("paths=%v wire=%q", run.paths, run.wire)
	}
}
