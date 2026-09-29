package test_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/zatrano/rawhttp"
)

// assertAttackRejected ensures malicious input never reaches the handler and
// yields the expected status class (400/413/417/431).
func assertAttackRejected(t *testing.T, name, req string, wantSubstr ...string) {
	t.Helper()
	handlerCalled := false
	srv := &rawhttp.Server{
		Handler: func(ctx *rawhttp.Ctx) {
			handlerCalled = true
			t.Errorf("%s: handler must not run (method=%q path=%q)", name, ctx.Method, ctx.Path)
		},
	}
	fc := newFakeConn([]byte(req))
	_ = srv.ServeConn(fc)
	resp := fc.w.String()
	if handlerCalled {
		t.Fatalf("%s: handler was called; resp=%q", name, resp)
	}
	if len(wantSubstr) == 0 {
		wantSubstr = []string{"400"}
	}
	for _, s := range wantSubstr {
		if !strings.Contains(resp, s) {
			t.Fatalf("%s: want %q in response, got %q", name, s, resp)
		}
	}
}

func TestSecurityAttackCorpus(t *testing.T) {
	cases := []struct {
		name string
		req  string
		want []string // substrings; default 400
	}{
		// --- Request smuggling / desync ---
		{
			name: "CL.TE conflict",
			req:  "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 4\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n0\r\n\r\n",
		},
		{
			name: "TE.CL conflict reversed order",
			req:  "POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\nContent-Length: 6\r\nConnection: close\r\n\r\n0\r\n\r\n",
		},
		{
			name: "duplicate differing Content-Length",
			req:  "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 1\r\nContent-Length: 2\r\nConnection: close\r\n\r\nx",
		},
		{
			name: "duplicate identical Content-Length",
			req:  "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 5\r\nContent-Length: 5\r\nConnection: close\r\n\r\nhello",
		},
		{
			name: "chunk size trailing garbage",
			req:  "POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n5 foo\r\nhello\r\n0\r\n\r\n",
		},
		{
			name: "duplicate Transfer-Encoding",
			req:  "POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n0\r\n\r\n",
		},
		{
			name: "TE identity obfuscation",
			req:  "POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: identity\r\nContent-Length: 0\r\nConnection: close\r\n\r\n",
		},
		{
			name: "TE gzip",
			req:  "POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: gzip\r\nConnection: close\r\n\r\n",
		},
		{
			name: "TE chunked, identity",
			req:  "POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked, identity\r\nConnection: close\r\n\r\n0\r\n\r\n",
		},
		{
			name: "TE chunked trailing junk",
			req:  "POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked trailing\r\nConnection: close\r\n\r\n0\r\n\r\n",
		},
		{
			name: "CL leading zero smuggling",
			req:  "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 05\r\nConnection: close\r\n\r\nhello",
		},
		{
			name: "CL overflow digits",
			req:  "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 999999999999999999999\r\nConnection: close\r\n\r\n",
		},
		{
			name: "CL negative-looking",
			req:  "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: -1\r\nConnection: close\r\n\r\n",
		},
		{
			name: "CL plus sign",
			req:  "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: +5\r\nConnection: close\r\n\r\nhello",
		},
		{
			name: "CL hex",
			req:  "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 0x5\r\nConnection: close\r\n\r\nhello",
		},
		{
			name: "CL empty",
			req:  "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length:\r\nConnection: close\r\n\r\n",
		},

		// --- GET/HEAD body / Expect abuse ---
		{
			name: "GET with Content-Length body",
			req:  "GET / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 5\r\nConnection: close\r\n\r\nhello",
		},
		{
			name: "HEAD with Content-Length body",
			req:  "HEAD / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 3\r\nConnection: close\r\n\r\nxxx",
		},
		{
			name: "GET with chunked body",
			req:  "GET / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n0\r\n\r\n",
		},
		{
			name: "Expect 100-continue on GET",
			req:  "GET / HTTP/1.1\r\nHost: localhost\r\nExpect: 100-continue\r\nConnection: close\r\n\r\n",
		},
		{
			name: "unsupported Expect",
			req:  "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 0\r\nExpect: watermelon\r\nConnection: close\r\n\r\n",
			want: []string{"417"},
		},

		// --- Host attacks ---
		{
			name: "missing Host HTTP/1.1",
			req:  "GET / HTTP/1.1\r\nConnection: close\r\n\r\n",
		},
		{
			name: "duplicate Host",
			req:  "GET / HTTP/1.1\r\nHost: a\r\nHost: b\r\nConnection: close\r\n\r\n",
		},
		{
			name: "Host with space",
			req:  "GET / HTTP/1.1\r\nHost: bad host\r\nConnection: close\r\n\r\n",
		},
		{
			name: "Host userinfo",
			req:  "GET / HTTP/1.1\r\nHost: user@evil\r\nConnection: close\r\n\r\n",
		},
		{
			name: "Host path confusion",
			req:  "GET / HTTP/1.1\r\nHost: evil/path\r\nConnection: close\r\n\r\n",
		},
		{
			name: "Host backslash",
			req:  "GET / HTTP/1.1\r\nHost: evil\\host\r\nConnection: close\r\n\r\n",
		},
		{
			name: "Host comma list",
			req:  "GET / HTTP/1.1\r\nHost: a,b\r\nConnection: close\r\n\r\n",
		},
		{
			name: "Host empty",
			req:  "GET / HTTP/1.1\r\nHost:\r\nConnection: close\r\n\r\n",
		},
		{
			name: "Host NUL",
			req:  "GET / HTTP/1.1\r\nHost: evil\x00host\r\nConnection: close\r\n\r\n",
		},
		{
			name: "Host non-ASCII",
			req:  "GET / HTTP/1.1\r\nHost: caf\xc3\xa9\r\nConnection: close\r\n\r\n",
		},
		{
			name: "Host too long",
			req:  "GET / HTTP/1.1\r\nHost: " + strings.Repeat("a", 300) + "\r\nConnection: close\r\n\r\n",
		},

		// --- Absolute / asterisk / version ---
		{
			name: "absolute-form target",
			req:  "GET http://evil/ HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n",
		},
		{
			name: "scheme-relative target",
			req:  "GET //evil/ HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n",
		},
		{
			name: "asterisk on GET",
			req:  "GET * HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n",
		},
		{
			name: "asterisk on POST",
			req:  "POST * HTTP/1.1\r\nHost: localhost\r\nContent-Length: 0\r\nConnection: close\r\n\r\n",
		},
		{
			name: "HTTP/2.0",
			req:  "GET / HTTP/2.0\r\nHost: localhost\r\nConnection: close\r\n\r\n",
		},
		{
			name: "HTTP/0.9 style",
			req:  "GET /\r\n",
		},
		{
			name: "HTTP/1.10",
			req:  "GET / HTTP/1.10\r\nHost: localhost\r\nConnection: close\r\n\r\n",
		},
		{
			name: "lowercase http version",
			req:  "GET / http/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n",
		},

		// --- Dangerous methods ---
		{name: "TRACE", req: "TRACE / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"},
		{name: "trace lower", req: "trace / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"},
		{name: "CONNECT", req: "CONNECT / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"},
		{name: "connect lower", req: "connect / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"},
		{name: "TRACK", req: "TRACK / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"}, // token but obscure; allowed if token — skip if passes
		{
			name: "method with space",
			req:  "GE T / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n",
		},
		{
			name: "method too long",
			req:  strings.Repeat("A", 64) + " / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n",
		},
		{
			name: "method with CTL",
			req:  "G\x01T / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n",
		},

		// --- Path attacks ---
		{name: "path backslash", req: "GET /a\\b HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"},
		{name: "path fragment", req: "GET /a#b HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"},
		{name: "path %00", req: "GET /a%00b HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"},
		{name: "path NUL", req: "GET /a\x00b HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"},
		{name: "path CR", req: "GET /a\rb HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"},
		{name: "path LF", req: "GET /a\nb HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"},
		{name: "dotdot root", req: "GET /../x HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"},
		{name: "dotdot mid", req: "GET /a/../b HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"},
		{name: "encoded dot %2e", req: "GET /a/%2e%2e/b HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"},
		{name: "encoded dot %2E", req: "GET /%2E%2E/x HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"},
		{name: "encoded single %2e", req: "GET /%2e HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"},
		{name: "dotdot end", req: "GET /a/.. HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"},
		{name: "dotdot only", req: "GET /.. HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"},

		// --- Header parsing / obs-fold / CTL / bare LF ---
		{
			name: "obs-fold",
			req:  "GET / HTTP/1.1\r\nHost: localhost\r\nX-A: 1\r\n folded\r\nConnection: close\r\n\r\n",
		},
		{
			name: "bare LF request line",
			req:  "GET / HTTP/1.1\nHost: localhost\n\n",
		},
		{
			name: "header value CTL",
			req:  "GET / HTTP/1.1\r\nHost: localhost\r\nX-Bad: a\x01b\r\nConnection: close\r\n\r\n",
		},
		{
			name: "header value DEL",
			req:  "GET / HTTP/1.1\r\nHost: localhost\r\nX-Bad: a\x7fb\r\nConnection: close\r\n\r\n",
		},
		{
			name: "header name space",
			req:  "GET / HTTP/1.1\r\nHost: localhost\r\nBad Name: x\r\nConnection: close\r\n\r\n",
		},
		{
			name: "header name NUL",
			req:  "GET / HTTP/1.1\r\nHost: localhost\r\nX\x00Bad: x\r\nConnection: close\r\n\r\n",
		},
		{
			name: "header name too long",
			req:  "GET / HTTP/1.1\r\nHost: localhost\r\n" + strings.Repeat("a", 300) + ": x\r\nConnection: close\r\n\r\n",
		},
		{
			name: "missing colon header",
			req:  "GET / HTTP/1.1\r\nHost: localhost\r\nNoColon\r\nConnection: close\r\n\r\n",
		},
		{
			name: "Upgrade websocket",
			req:  "GET / HTTP/1.1\r\nHost: localhost\r\nUpgrade: websocket\r\nConnection: close\r\n\r\n",
		},
		{
			name: "Connection upgrade token",
			req:  "GET / HTTP/1.1\r\nHost: localhost\r\nConnection: keep-alive, upgrade\r\n\r\n",
		},
		{
			name: "TE trailers",
			req:  "GET / HTTP/1.1\r\nHost: localhost\r\nTE: trailers\r\nConnection: close\r\n\r\n",
		},
		{
			name: "Proxy-Connection",
			req:  "GET / HTTP/1.1\r\nHost: localhost\r\nProxy-Connection: keep-alive\r\nConnection: close\r\n\r\n",
		},

		// --- Chunked body attacks ---
		{
			name: "chunk extension too long",
			req: "POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n" +
				"0;" + strings.Repeat("a", 300) + "\r\n\r\n",
		},
		{
			name: "chunk extension CTL",
			req: "POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n" +
				"0;a\x01b\r\n\r\n",
		},
		{
			name: "chunk hex too wide",
			req: "POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n" +
				"00000000000000001\r\nX\r\n0\r\n\r\n",
		},
		{
			name: "chunk size not hex",
			req: "POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n" +
				"GZ\r\nxx\r\n0\r\n\r\n",
		},
		{
			name: "chunk missing CRLF after data",
			req: "POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n" +
				"1\r\nZ0\r\n\r\n",
		},
		{
			name: "forbidden trailer Content-Length",
			req: "POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n" +
				"0\r\nContent-Length: 5\r\n\r\n",
		},
		{
			name: "forbidden trailer Transfer-Encoding",
			req: "POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n" +
				"0\r\nTransfer-Encoding: chunked\r\n\r\n",
		},
		{
			name: "forbidden trailer Host",
			req: "POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n" +
				"0\r\nHost: evil\r\n\r\n",
		},
		{
			name: "forbidden trailer Connection",
			req: "POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n" +
				"0\r\nConnection: close\r\n\r\n",
		},
		{
			name: "forbidden trailer Upgrade",
			req: "POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n" +
				"0\r\nUpgrade: h2c\r\n\r\n",
		},
		{
			name: "trailer obs-fold",
			req: "POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n" +
				"0\r\nX-A: 1\r\n folded\r\n\r\n",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			// TRACK is an obscure token method — rawhttp may accept it as a
			// generic method token. Skip if we intentionally allow unknown methods.
			if tc.name == "TRACK" {
				srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) { ctx.SetBodyString("ok") }}
				fc := newFakeConn([]byte(tc.req))
				_ = srv.ServeConn(fc)
				resp := fc.w.String()
				// Either reject (400) or handle safely without crash — both OK.
				if strings.Contains(resp, "500") {
					t.Fatalf("TRACK caused 500: %q", resp)
				}
				return
			}
			assertAttackRejected(t, tc.name, tc.req, tc.want...)
		})
	}
}

func TestSecurityMaxChunksDoS(t *testing.T) {
	var b strings.Builder
	b.WriteString("POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n")
	for i := 0; i < 5; i++ {
		b.WriteString("1\r\nZ\r\n")
	}
	b.WriteString("0\r\n\r\n")
	srv := &rawhttp.Server{
		MaxChunks: 3,
		Handler:   func(ctx *rawhttp.Ctx) { t.Fatal("handler") },
	}
	fc := newFakeConn([]byte(b.String()))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "400") {
		t.Fatalf("expected 400, got %q", fc.w.String())
	}
}

func TestSecurityHeaderCountBomb(t *testing.T) {
	var b strings.Builder
	b.WriteString("GET / HTTP/1.1\r\nHost: localhost\r\n")
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&b, "X-%d: 1\r\n", i)
	}
	b.WriteString("Connection: close\r\n\r\n")
	srv := &rawhttp.Server{
		MaxHeaders: 5,
		Handler:    func(ctx *rawhttp.Ctx) { t.Fatal("handler") },
	}
	fc := newFakeConn([]byte(b.String()))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "400") {
		t.Fatalf("expected 400, got %q", fc.w.String())
	}
}

func TestSecurityHeaderBytesBomb(t *testing.T) {
	req := "GET / HTTP/1.1\r\nHost: localhost\r\nX: " + strings.Repeat("a", 500) + "\r\nConnection: close\r\n\r\n"
	srv := &rawhttp.Server{
		MaxHeaderBytes: 128,
		Handler:        func(ctx *rawhttp.Ctx) { t.Fatal("handler") },
	}
	fc := newFakeConn([]byte(req))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "431") {
		t.Fatalf("expected 431, got %q", fc.w.String())
	}
}

func TestSecurityBodyTooLarge(t *testing.T) {
	req := "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 100\r\nConnection: close\r\n\r\n" + strings.Repeat("x", 100)
	srv := &rawhttp.Server{
		MaxRequestBodySize: 16,
		Handler:            func(ctx *rawhttp.Ctx) { t.Fatal("handler") },
	}
	fc := newFakeConn([]byte(req))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "413") {
		t.Fatalf("expected 413, got %q", fc.w.String())
	}
}

func TestSecurityChunkedBodyTooLarge(t *testing.T) {
	req := "POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n" +
		"20\r\n" + strings.Repeat("x", 32) + "\r\n0\r\n\r\n"
	srv := &rawhttp.Server{
		MaxRequestBodySize: 8,
		Handler:            func(ctx *rawhttp.Ctx) { t.Fatal("handler") },
	}
	fc := newFakeConn([]byte(req))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "413") {
		t.Fatalf("expected 413, got %q", fc.w.String())
	}
}

func TestSecurityResponseSplittingBlocked(t *testing.T) {
	req := "GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) {
		attacks := [][2]string{
			{"X-A", "1\r\nX-Injected: yes"},
			{"X-A", "1\nX-Injected: yes"},
			{"X-A", "1\x00yes"},
			{"X-A\r\nX-B", "1"},
			{"X A", "1"},
		}
		for _, a := range attacks {
			if err := ctx.SetHeader(a[0], a[1]); err == nil {
				t.Fatalf("SetHeader(%q,%q) should fail", a[0], a[1])
			}
		}
		ctx.SetContentType("text/plain\r\nX-Injected: yes")
		ctx.SetBodyString("ok")
	}}
	fc := newFakeConn([]byte(req))
	if err := srv.ServeConn(fc); err != nil {
		t.Fatal(err)
	}
	resp := fc.w.String()
	if strings.Contains(resp, "X-Injected") {
		t.Fatalf("response splitting succeeded: %q", resp)
	}
	if !strings.Contains(resp, "ok") {
		t.Fatalf("expected body ok, got %q", resp)
	}
}

func TestSecurityPipelinedSmuggleSecondRequestNotHandledAsFirst(t *testing.T) {
	// Malformed first request must not let a smuggled second request hit the handler.
	req := "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 1\r\nContent-Length: 2\r\nConnection: close\r\n\r\n" +
		"xGET /smuggled HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
	handlerCalled := false
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) {
		handlerCalled = true
		t.Errorf("handler saw %q", ctx.Path)
	}}
	fc := newFakeConn([]byte(req))
	_ = srv.ServeConn(fc)
	if handlerCalled {
		t.Fatal("smuggled request reached handler")
	}
	if !strings.Contains(fc.w.String(), "400") {
		t.Fatalf("expected 400, got %q", fc.w.String())
	}
}

func TestSecurityLegitimateTrafficStillWorks(t *testing.T) {
	cases := []struct {
		name string
		req  string
		want string
	}{
		{
			name: "simple GET",
			req:  "GET /hello HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n",
			want: "hello-ok",
		},
		{
			name: "POST CL",
			req:  "POST /echo HTTP/1.1\r\nHost: localhost\r\nContent-Length: 3\r\nConnection: close\r\n\r\nxyz",
			want: "xyz",
		},
		{
			name: "POST chunked",
			req:  "POST /echo HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n3\r\nabc\r\n0\r\n\r\n",
			want: "abc",
		},
		{
			name: "OPTIONS asterisk",
			req:  "OPTIONS * HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n",
			want: "options",
		},
		{
			name: "path with dotfile not traversal",
			req:  "GET /.well-known/acme HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n",
			want: "dotok",
		},
		{
			name: "path with .. in query allowed",
			req:  "GET /ok?path=../x HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n",
			want: "queryok",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) {
				switch {
				case ctx.PathEqual("/hello"):
					ctx.SetBodyString("hello-ok")
				case ctx.PathEqual("/echo"):
					ctx.SetBody(ctx.Body())
				case ctx.PathEqual("*"):
					ctx.SetBodyString("options")
				case ctx.PathHasPrefix("/.well-known/"):
					ctx.SetBodyString("dotok")
				case ctx.PathEqual("/ok"):
					ctx.SetBodyString("queryok")
				default:
					ctx.NotFound()
				}
			}}
			fc := newFakeConn([]byte(tc.req))
			if err := srv.ServeConn(fc); err != nil {
				t.Fatal(err)
			}
			resp := fc.w.String()
			if strings.Contains(resp, " 400 ") || strings.HasPrefix(resp, "HTTP/1.1 400") {
				t.Fatalf("legitimate request rejected: %q", resp)
			}
			if !strings.Contains(resp, tc.want) {
				t.Fatalf("want %q in %q", tc.want, resp)
			}
		})
	}
}

func TestSecurityErrorCallbackFiresOnAttacks(t *testing.T) {
	var n int
	srv := &rawhttp.Server{
		ErrorCallback: func(err error) { n++ },
		Handler:       func(ctx *rawhttp.Ctx) { t.Fatal("handler") },
	}
	attacks := []string{
		"GET / HTTP/1.1\r\nConnection: close\r\n\r\n",
		"GET /../x HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n",
		"TRACE / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n",
	}
	for _, req := range attacks {
		fc := newFakeConn([]byte(req))
		_ = srv.ServeConn(fc)
	}
	if n != len(attacks) {
		t.Fatalf("ErrorCallback count=%d want=%d", n, len(attacks))
	}
}
