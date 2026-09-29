package test_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/zatrano/rawhttp"
)

func TestHijack(t *testing.T) {
	req := "GET /hijack HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\nEXTRA"
	var gotExtra string
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) {
		conn, leftover, err := ctx.Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		gotExtra = string(leftover)
		_, _ = conn.Write([]byte("HIJACKED"))
		_ = conn.Close()
	}}
	fc := newFakeConn([]byte(req))
	if err := srv.ServeConn(fc); err != nil {
		t.Fatal(err)
	}
	if gotExtra != "EXTRA" {
		t.Fatalf("leftover=%q", gotExtra)
	}
	if !strings.Contains(fc.w.String(), "HIJACKED") {
		t.Fatalf("response %q", fc.w.String())
	}
	if strings.Contains(fc.w.String(), "HTTP/1.1") {
		t.Fatalf("server should not write HTTP response after hijack: %q", fc.w.String())
	}
}

func TestTimeoutHandler(t *testing.T) {
	req := "GET /slow HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
	h := rawhttp.TimeoutHandler(func(ctx *rawhttp.Ctx) {
		time.Sleep(80 * time.Millisecond)
		ctx.SetBody([]byte("too-late"))
	}, 20*time.Millisecond, "deadline")
	srv := &rawhttp.Server{Handler: h}
	fc := newFakeConn([]byte(req))
	if err := srv.ServeConn(fc); err != nil {
		t.Fatal(err)
	}
	resp := fc.w.String()
	if !strings.Contains(resp, "503") {
		t.Fatalf("want 503: %q", resp)
	}
	if !strings.Contains(resp, "deadline") {
		t.Fatalf("want timeout body: %q", resp)
	}
	if strings.Contains(resp, "too-late") {
		t.Fatalf("timed-out handler body leaked: %q", resp)
	}
}

func TestCookieRoundTrip(t *testing.T) {
	req := "GET / HTTP/1.1\r\nHost: localhost\r\nCookie: a=1; session=abc\r\nConnection: close\r\n\r\n"
	var got []byte
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) {
		got = append([]byte(nil), ctx.Cookie("session")...)
		_ = ctx.SetCookie(&rawhttp.Cookie{
			Name:     "session",
			Value:    "xyz",
			Path:     "/",
			HTTPOnly: true,
			SameSite: rawhttp.SameSiteLaxMode,
		})
		ctx.SetBody([]byte("ok"))
	}}
	fc := newFakeConn([]byte(req))
	if err := srv.ServeConn(fc); err != nil {
		t.Fatal(err)
	}
	if string(got) != "abc" {
		t.Fatalf("cookie=%q", got)
	}
	resp := fc.w.String()
	if !strings.Contains(resp, "Set-Cookie: session=xyz; Path=/; HttpOnly; SameSite=Lax\r\n") {
		t.Fatalf("set-cookie missing: %q", resp)
	}
}

func TestPostArgsForm(t *testing.T) {
	body := "name=Ada&city=Istanbul"
	req := "POST /form HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/x-www-form-urlencoded\r\nContent-Length: " +
		itoa(len(body)) + "\r\nConnection: close\r\n\r\n" + body
	var name, city string
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) {
		name = string(ctx.PostArgs().Get("name"))
		city = string(ctx.FormValue("city"))
		ctx.SetBody([]byte("ok"))
	}}
	fc := newFakeConn([]byte(req))
	if err := srv.ServeConn(fc); err != nil {
		t.Fatal(err)
	}
	if name != "Ada" || city != "Istanbul" {
		t.Fatalf("name=%q city=%q", name, city)
	}
}

func TestSetBodyStream(t *testing.T) {
	req := "GET /stream HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
	payload := []byte("stream-body-data")
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) {
		ctx.SetContentType("application/octet-stream")
		ctx.SetBodyStream(bytes.NewReader(payload), len(payload))
	}}
	fc := newFakeConn([]byte(req))
	if err := srv.ServeConn(fc); err != nil {
		t.Fatal(err)
	}
	resp := fc.w.String()
	if !strings.Contains(resp, "Content-Length: 16\r\n") {
		t.Fatalf("cl: %q", resp)
	}
	if !strings.HasSuffix(resp, string(payload)) {
		t.Fatalf("body: %q", resp)
	}
}

func TestSetBodyStreamChunked(t *testing.T) {
	req := "GET /stream HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) {
		ctx.SetBodyStream(strings.NewReader("abcdefghij"), -1)
	}}
	fc := newFakeConn([]byte(req))
	if err := srv.ServeConn(fc); err != nil {
		t.Fatal(err)
	}
	resp := fc.w.String()
	if !strings.Contains(resp, "Transfer-Encoding: chunked\r\n") {
		t.Fatalf("te: %q", resp)
	}
	if !strings.Contains(resp, "abcdefghij") {
		t.Fatalf("chunked body: %q", resp)
	}
}

func TestDelHeader(t *testing.T) {
	req := "GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) {
		_ = ctx.SetHeader("X-A", "1")
		_ = ctx.SetHeader("X-B", "2")
		_ = ctx.AddHeader("X-A", "3")
		ctx.DelHeader("X-A")
		ctx.SetBody([]byte("ok"))
	}}
	fc := newFakeConn([]byte(req))
	if err := srv.ServeConn(fc); err != nil {
		t.Fatal(err)
	}
	resp := fc.w.String()
	if strings.Contains(resp, "X-A:") {
		t.Fatalf("X-A should be deleted: %q", resp)
	}
	if !strings.Contains(resp, "X-B: 2\r\n") {
		t.Fatalf("X-B missing: %q", resp)
	}
}

func TestPeekHeader(t *testing.T) {
	req := "GET / HTTP/1.1\r\nHost: example.test\r\nUser-Agent: ua\r\nConnection: close\r\n\r\n"
	var host, ua string
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) {
		host = string(ctx.Peek("Host"))
		ua = string(ctx.Peek("User-Agent"))
		ctx.SetBody([]byte("ok"))
	}}
	fc := newFakeConn([]byte(req))
	if err := srv.ServeConn(fc); err != nil {
		t.Fatal(err)
	}
	if host != "example.test" || ua != "ua" {
		t.Fatalf("host=%q ua=%q", host, ua)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [16]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
