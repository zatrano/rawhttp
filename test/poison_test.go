//go:build rawhttp_poison

package test_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/zatrano/rawhttp"
)

// Retain request slices across handler return; with -tags rawhttp_poison they
// must read as 0xDE (debug fill of the pinned buffer / owned copies).

func assertPoisoned(t *testing.T, name string, b []byte, wantLen int) {
	t.Helper()
	if wantLen > 0 && len(b) != wantLen {
		t.Fatalf("%s: retained len=%d want %d", name, len(b), wantLen)
	}
	if len(b) == 0 {
		t.Fatalf("%s: retained empty slice", name)
	}
	for i, c := range b {
		if c != 0xDE {
			t.Fatalf("%s: byte[%d]=0x%02X want 0xDE (slice=%q)", name, i, c, b)
		}
	}
}

func serveRetain(t *testing.T, raw string, retain func(ctx *rawhttp.Ctx) []byte) []byte {
	t.Helper()
	var held []byte
	var during []byte
	srv := &rawhttp.Server{
		ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
		Handler: func(ctx *rawhttp.Ctx) {
			held = retain(ctx)
			during = append([]byte(nil), held...)
			ctx.SetBody([]byte("ok"))
		},
	}
	fc := newFakeConn([]byte(raw))
	_ = srv.ServeConn(fc)
	if len(during) == 0 {
		t.Fatal("handler did not capture a non-empty slice")
	}
	for _, c := range during {
		if c == 0xDE {
			t.Fatalf("slice already poisoned inside handler: %q", during)
		}
	}
	return held
}

func TestPoison_Method(t *testing.T) {
	held := serveRetain(t,
		"GET /p HTTP/1.1\r\nHost: h\r\n\r\n",
		func(ctx *rawhttp.Ctx) []byte { return ctx.Method },
	)
	assertPoisoned(t, "Method", held, 3)
}

func TestPoison_Path(t *testing.T) {
	held := serveRetain(t,
		"GET /poison-path HTTP/1.1\r\nHost: h\r\n\r\n",
		func(ctx *rawhttp.Ctx) []byte { return ctx.Path },
	)
	assertPoisoned(t, "Path", held, len("/poison-path"))
}

func TestPoison_RequestURI(t *testing.T) {
	held := serveRetain(t,
		"GET /u?x=1 HTTP/1.1\r\nHost: h\r\n\r\n",
		func(ctx *rawhttp.Ctx) []byte { return ctx.RequestURI() },
	)
	assertPoisoned(t, "RequestURI", held, len("/u?x=1"))
}

func TestPoison_Host(t *testing.T) {
	held := serveRetain(t,
		"GET / HTTP/1.1\r\nHost: poison.example\r\n\r\n",
		func(ctx *rawhttp.Ctx) []byte { return ctx.Host() },
	)
	assertPoisoned(t, "Host", held, len("poison.example"))
}

func TestPoison_Header(t *testing.T) {
	held := serveRetain(t,
		"GET / HTTP/1.1\r\nHost: h\r\nX-Trace: alpha\r\n\r\n",
		func(ctx *rawhttp.Ctx) []byte { return ctx.Header("X-Trace") },
	)
	assertPoisoned(t, "Header(X-Trace)", held, len("alpha"))
}

func TestPoison_Cookie(t *testing.T) {
	held := serveRetain(t,
		"GET / HTTP/1.1\r\nHost: h\r\nCookie: sid=abc123\r\n\r\n",
		func(ctx *rawhttp.Ctx) []byte { return ctx.Cookie("sid") },
	)
	assertPoisoned(t, "Cookie(sid)", held, len("abc123"))
}

func TestPoison_Query(t *testing.T) {
	held := serveRetain(t,
		"GET /q?name=rawhttp HTTP/1.1\r\nHost: h\r\n\r\n",
		func(ctx *rawhttp.Ctx) []byte { return ctx.Query },
	)
	assertPoisoned(t, "Query", held, len("name=rawhttp"))
}

func TestPoison_Body(t *testing.T) {
	held := serveRetain(t,
		"POST / HTTP/1.1\r\nHost: h\r\nContent-Length: 5\r\n\r\nhello",
		func(ctx *rawhttp.Ctx) []byte { return ctx.Body() },
	)
	assertPoisoned(t, "Body", held, 5)
}

func TestPoison_HandlerInternalIntactOnFragmented(t *testing.T) {
	// Same contract as fragmented_slices: values stable inside the handler.
	const req1 = "GET /one HTTP/1.1\r\nHost: a.example\r\nX-Trace: alpha\r\n\r\n"
	const req2 = "GET /two HTTP/1.1\r\nHost: b.example\r\nX-Trace: bravo\r\n\r\n"
	var saw []string
	srv := &rawhttp.Server{
		ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
		Handler: func(ctx *rawhttp.Ctx) {
			m, p, h, tr := ctx.Method, ctx.Path, ctx.Host(), ctx.Header("X-Trace")
			em, ep, eh, et := string(m), string(p), string(h), string(tr)
			if string(m) != em || string(p) != ep || string(h) != eh || string(tr) != et {
				t.Errorf("mutated inside handler")
			}
			saw = append(saw, ep+"|"+eh+"|"+et)
			ctx.SetBody([]byte("ok"))
		},
	}
	conn := newFragmentConn([]byte(req1+req2), 3)
	_ = srv.ServeConn(conn)
	if len(saw) != 2 || saw[0] != "/one|a.example|alpha" || saw[1] != "/two|b.example|bravo" {
		t.Fatalf("saw=%v", saw)
	}
}

func TestPoison_BuildEnabled(t *testing.T) {
	if !rawhttp.PoisonBuildEnabled() {
		t.Fatal("expected PoisonBuildEnabled with -tags rawhttp_poison")
	}
}

func echoPoisonHandler(ctx *rawhttp.Ctx) {
	path := string(ctx.Path)
	host := string(ctx.Host())
	trace := string(ctx.Header("X-Trace"))
	q := string(ctx.QueryArgs().Get("q"))
	ck := string(ctx.Cookie("sid"))
	body := path + "|" + host + "|" + trace + "|" + q + "|" + ck
	_ = ctx.SetHeader("X-Echo-Path", path)
	_ = ctx.SetHeader("X-Echo-Host", host)
	_ = ctx.SetHeader("X-Echo-Trace", trace)
	_ = ctx.SetHeader("X-Echo-Q", q)
	_ = ctx.SetHeader("X-Echo-Sid", ck)
	ctx.SetBodyString(body)
}

func assertEchoResponse(t *testing.T, resp, wantBody string) {
	t.Helper()
	if strings.Contains(resp, "\xde") || strings.Contains(resp, string([]byte{0xDE})) {
		t.Fatalf("response contains 0xDE poison byte: %q", resp)
	}
	if !strings.Contains(resp, "200") {
		t.Fatalf("want 200, got %q", resp)
	}
	if !strings.Contains(resp, wantBody) {
		t.Fatalf("want body %q in %q", wantBody, resp)
	}
	for _, h := range []string{"X-Echo-Path: /echo", "X-Echo-Host: h.example", "X-Echo-Trace: alpha", "X-Echo-Q: 1", "X-Echo-Sid: abc"} {
		if !strings.Contains(resp, h) {
			t.Fatalf("missing header %q in %q", h, resp)
		}
	}
}

const poisonEchoReq = "GET /echo?q=1 HTTP/1.1\r\nHost: h.example\r\nX-Trace: alpha\r\nCookie: sid=abc\r\n\r\n"
const poisonEchoWant = "/echo|h.example|alpha|1|abc"

func TestPoison_Echo_SingleRequest(t *testing.T) {
	srv := &rawhttp.Server{ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1, Handler: echoPoisonHandler}
	fc := newFakeConn([]byte(poisonEchoReq))
	_ = srv.ServeConn(fc)
	assertEchoResponse(t, fc.w.String(), poisonEchoWant)
}

func TestPoison_Echo_Pipelined(t *testing.T) {
	req2 := "GET /echo?q=1 HTTP/1.1\r\nHost: h.example\r\nX-Trace: alpha\r\nCookie: sid=abc\r\nConnection: close\r\n\r\n"
	srv := &rawhttp.Server{ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1, Handler: echoPoisonHandler}
	fc := newFakeConn([]byte(poisonEchoReq + req2))
	_ = srv.ServeConn(fc)
	resp := fc.w.String()
	if strings.Contains(resp, "\xde") {
		t.Fatalf("pipelined response contains 0xDE: %q", resp)
	}
	n := strings.Count(resp, poisonEchoWant)
	if n != 2 {
		t.Fatalf("want 2 echo bodies, got %d in %q", n, resp)
	}
}

func TestPoison_Echo_Fragmented(t *testing.T) {
	srv := &rawhttp.Server{ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1, Handler: echoPoisonHandler}
	fc := newFragmentConn([]byte(poisonEchoReq), 1)
	_ = srv.ServeConn(fc)
	assertEchoResponse(t, fc.w.String(), poisonEchoWant)
}

func TestPoison_PipelinedSecondRequestIntact(t *testing.T) {
	req1 := "GET /one HTTP/1.1\r\nHost: a.example\r\nX-Id: first\r\n\r\n"
	req2 := "GET /two HTTP/1.1\r\nHost: b.example\r\nX-Id: second\r\nConnection: close\r\n\r\n"
	var saw []string
	srv := &rawhttp.Server{
		ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
		Handler: func(ctx *rawhttp.Ctx) {
			saw = append(saw, string(ctx.Path)+"|"+string(ctx.Host())+"|"+string(ctx.Header("X-Id")))
			ctx.SetBody([]byte("ok"))
		},
	}
	fc := newFakeConn([]byte(req1 + req2))
	_ = srv.ServeConn(fc)
	if len(saw) != 2 {
		t.Fatalf("want 2 handlers, got %d %v; resp=%q", len(saw), saw, fc.w.String())
	}
	if saw[0] != "/one|a.example|first" || saw[1] != "/two|b.example|second" {
		t.Fatalf("second request corrupted under poison: %v", saw)
	}
	if strings.Contains(fc.w.String(), "\xde") {
		t.Fatalf("response poisoned: %q", fc.w.String())
	}
}

func TestPoison_VisitHeaderRetained(t *testing.T) {
	var keys, vals [][]byte
	srv := &rawhttp.Server{
		ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
		Handler: func(ctx *rawhttp.Ctx) {
			ctx.VisitHeader(func(k, v []byte) {
				keys = append(keys, k)
				vals = append(vals, v)
			})
			ctx.SetBody([]byte("ok"))
		},
	}
	fc := newFakeConn([]byte("GET / HTTP/1.1\r\nHost: h\r\nX-Trace: zz\r\n\r\n"))
	_ = srv.ServeConn(fc)
	if len(keys) == 0 {
		t.Fatal("no headers visited")
	}
	for i := range keys {
		assertPoisoned(t, "VisitHeader key", keys[i], 0)
		assertPoisoned(t, "VisitHeader val", vals[i], 0)
	}
}

func TestPoison_VisitCookieRetained(t *testing.T) {
	var names, vals [][]byte
	srv := &rawhttp.Server{
		ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
		Handler: func(ctx *rawhttp.Ctx) {
			ctx.VisitCookie(func(n, v []byte) {
				names = append(names, n)
				vals = append(vals, v)
			})
			ctx.SetBody([]byte("ok"))
		},
	}
	fc := newFakeConn([]byte("GET / HTTP/1.1\r\nHost: h\r\nCookie: a=1; b=2\r\n\r\n"))
	_ = srv.ServeConn(fc)
	if len(names) < 2 {
		t.Fatalf("want ≥2 cookies, got %d", len(names))
	}
	for i := range names {
		assertPoisoned(t, "VisitCookie name", names[i], 0)
		assertPoisoned(t, "VisitCookie val", vals[i], 0)
	}
}

func TestPoison_QueryArgsRetained(t *testing.T) {
	var held []byte
	srv := &rawhttp.Server{
		ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
		Handler: func(ctx *rawhttp.Ctx) {
			held = ctx.QueryArgs().Get("name")
			ctx.SetBody([]byte("ok"))
		},
	}
	fc := newFakeConn([]byte("GET /?name=raw HTTP/1.1\r\nHost: h\r\n\r\n"))
	_ = srv.ServeConn(fc)
	assertPoisoned(t, "QueryArgs.Get", held, 3)
}

func TestPoison_PostArgsRetained(t *testing.T) {
	var held []byte
	srv := &rawhttp.Server{
		ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
		Handler: func(ctx *rawhttp.Ctx) {
			held = ctx.PostArgs().Get("f")
			ctx.SetBody([]byte("ok"))
		},
	}
	body := "f=hello"
	req := "POST / HTTP/1.1\r\nHost: h\r\nContent-Type: application/x-www-form-urlencoded\r\nContent-Length: " +
		strconv.Itoa(len(body)) + "\r\n\r\n" + body
	fc := newFakeConn([]byte(req))
	_ = srv.ServeConn(fc)
	assertPoisoned(t, "PostArgs.Get", held, 5)
}
