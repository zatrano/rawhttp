package test_test

import (
	"encoding/base64"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zatrano/rawhttp"
)

func TestBasicAuthMiddleware(t *testing.T) {
	h := rawhttp.BasicAuthMiddleware(func(ctx *rawhttp.Ctx) {
		ctx.SetBody([]byte("secret"))
	}, "alice", "s3cret", "api")
	srv := &rawhttp.Server{Handler: h, ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1}

	fc := newFakeConn([]byte("GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "401") || !strings.Contains(fc.w.String(), "WWW-Authenticate") {
		t.Fatalf("want 401: %q", fc.w.String())
	}

	cred := base64.StdEncoding.EncodeToString([]byte("alice:s3cret"))
	fc = newFakeConn([]byte("GET / HTTP/1.1\r\nHost: localhost\r\nAuthorization: Basic " + cred + "\r\nConnection: close\r\n\r\n"))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "secret") {
		t.Fatalf("want body: %q", fc.w.String())
	}
}

func TestRequestIDMiddleware(t *testing.T) {
	var got any
	h := rawhttp.RequestIDMiddleware(func(ctx *rawhttp.Ctx) {
		got = ctx.UserValue("X-Request-ID")
		ctx.SetBody([]byte("ok"))
	}, rawhttp.RequestIDOptions{})
	srv := &rawhttp.Server{Handler: h, ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1}
	fc := newFakeConn([]byte("GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"))
	_ = srv.ServeConn(fc)
	resp := fc.w.String()
	if !strings.Contains(resp, "X-Request-ID: ") {
		t.Fatalf("missing id header: %q", resp)
	}
	if got == nil || got.(string) == "" {
		t.Fatalf("UserValue id=%v", got)
	}
}

func TestAccessLogMiddleware(t *testing.T) {
	var status int
	var dur time.Duration
	h := rawhttp.AccessLogMiddleware(func(ctx *rawhttp.Ctx) {
		ctx.SetStatusCode(201)
		ctx.SetBody([]byte("ok"))
	}, func(ctx *rawhttp.Ctx, code int, d time.Duration) {
		status = code
		dur = d
	})
	srv := &rawhttp.Server{Handler: h, ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1}
	fc := newFakeConn([]byte("GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"))
	_ = srv.ServeConn(fc)
	if status != 201 || dur < 0 {
		t.Fatalf("status=%d dur=%v", status, dur)
	}
}

func TestQueryArgsHelpersAndMethods(t *testing.T) {
	var u uint64
	var ok bool
	var b bool
	var s string
	var isGet, isPost bool
	srv := &rawhttp.Server{
		Handler: func(ctx *rawhttp.Ctx) {
			isGet = ctx.IsGet()
			isPost = ctx.IsPost()
			qa := ctx.QueryArgs()
			u, ok = qa.GetUint("n")
			b = qa.GetBool("flag")
			s = qa.GetString("name")
			ctx.SetBody([]byte("ok"))
		},
		ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
	}
	fc := newFakeConn([]byte("GET /?n=42&flag=true&name=bob HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"))
	_ = srv.ServeConn(fc)
	if !isGet || isPost || !ok || u != 42 || !b || s != "bob" {
		t.Fatalf("get=%v post=%v u=%d ok=%v b=%v s=%q", isGet, isPost, u, ok, b, s)
	}
}

func TestAllowedHostsTrailingDot(t *testing.T) {
	srv := &rawhttp.Server{
		AllowedHosts: []string{"app.local"},
		Handler:      func(ctx *rawhttp.Ctx) { ctx.SetBody([]byte("ok")) },
		ReadTimeout:  -1, WriteTimeout: -1, IdleTimeout: -1,
	}
	fc := newFakeConn([]byte("GET / HTTP/1.1\r\nHost: app.local.\r\nConnection: close\r\n\r\n"))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "ok") {
		t.Fatalf("trailing dot should match: %q", fc.w.String())
	}
}

func TestClientDoDeadline(t *testing.T) {
	var hits atomic.Int32
	addr, stop := startTestServer(t, func(ctx *rawhttp.Ctx) {
		hits.Add(1)
		ctx.SetBody([]byte("ok"))
	})
	defer stop()
	c := &rawhttp.Client{}
	req := rawhttp.AcquireRequest()
	defer rawhttp.ReleaseRequest(req)
	req.Method = "GET"
	req.RequestURI = "http://" + addr + "/"
	resp := rawhttp.AcquireResponse()
	defer rawhttp.ReleaseResponse(resp)
	if err := c.DoDeadline(req, resp, time.Now().Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || string(resp.Body()) != "ok" || hits.Load() != 1 {
		t.Fatalf("status=%d body=%q hits=%d", resp.StatusCode, resp.Body(), hits.Load())
	}
	if err := c.DoDeadline(req, resp, time.Now().Add(-time.Second)); err != rawhttp.ErrTimeout {
		t.Fatalf("want ErrTimeout, got %v", err)
	}
}

func TestStripPrefixMiddleware(t *testing.T) {
	var path string
	h := rawhttp.StripPrefixMiddleware("/api", func(ctx *rawhttp.Ctx) {
		path = string(ctx.Path)
		ctx.SetBody([]byte("ok"))
	})
	srv := &rawhttp.Server{Handler: h, ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1}
	fc := newFakeConn([]byte("GET /api/v1 HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"))
	_ = srv.ServeConn(fc)
	if path != "/v1" || !strings.Contains(fc.w.String(), "ok") {
		t.Fatalf("path=%q resp=%q", path, fc.w.String())
	}
	fc = newFakeConn([]byte("GET /other HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "404") {
		t.Fatalf("want 404: %q", fc.w.String())
	}
}

func TestMethodOverrideMiddleware(t *testing.T) {
	var method string
	h := rawhttp.MethodOverrideMiddleware(func(ctx *rawhttp.Ctx) {
		method = string(ctx.Method)
		ctx.SetBody([]byte("ok"))
	})
	srv := &rawhttp.Server{Handler: h, ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1}
	fc := newFakeConn([]byte(
		"POST / HTTP/1.1\r\nHost: localhost\r\nX-HTTP-Method-Override: DELETE\r\nContent-Length: 0\r\nConnection: close\r\n\r\n",
	))
	_ = srv.ServeConn(fc)
	if method != "DELETE" {
		t.Fatalf("method=%q", method)
	}
}

func TestNoCacheAndVisitCookie(t *testing.T) {
	var names []string
	h := rawhttp.NoCacheMiddleware(func(ctx *rawhttp.Ctx) {
		ctx.VisitCookie(func(name, val []byte) {
			names = append(names, string(name)+"="+string(val))
		})
		ctx.SetBody([]byte("ok"))
	})
	srv := &rawhttp.Server{Handler: h, ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1}
	fc := newFakeConn([]byte(
		"GET / HTTP/1.1\r\nHost: localhost\r\nCookie: a=1; b=2\r\nConnection: close\r\n\r\n",
	))
	_ = srv.ServeConn(fc)
	resp := fc.w.String()
	if !strings.Contains(resp, "Cache-Control: no-store") {
		t.Fatalf("want no-store: %q", resp)
	}
	if len(names) != 2 || names[0] != "a=1" || names[1] != "b=2" {
		t.Fatalf("cookies=%v", names)
	}
}

func TestHeadOrGetAndNormalizePath(t *testing.T) {
	var path string
	h := rawhttp.NormalizePathMiddleware(rawhttp.HeadOrGetMiddleware(func(ctx *rawhttp.Ctx) {
		path = string(ctx.Path)
		ctx.SetBodyString("ok")
	}))
	srv := &rawhttp.Server{Handler: h, ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1}

	fc := newFakeConn([]byte("GET /a///b HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"))
	_ = srv.ServeConn(fc)
	if path != "/a/b" {
		t.Fatalf("path=%q resp=%q", path, fc.w.String())
	}
	if !strings.Contains(fc.w.String(), "ok") {
		t.Fatalf("body: %q", fc.w.String())
	}

	fc = newFakeConn([]byte("POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "405") {
		t.Fatalf("want 405: %q", fc.w.String())
	}
}

func TestTimeoutMiddlewareAlias(t *testing.T) {
	h := rawhttp.TimeoutMiddleware(func(ctx *rawhttp.Ctx) {
		time.Sleep(50 * time.Millisecond)
		ctx.SetBodyString("late")
	}, 5*time.Millisecond, "slow")
	srv := &rawhttp.Server{Handler: h, ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1}
	fc := newFakeConn([]byte("GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "503") || !strings.Contains(fc.w.String(), "slow") {
		t.Fatalf("want timeout: %q", fc.w.String())
	}
}

func TestRequireContentTypeMiddleware(t *testing.T) {
	h := rawhttp.RequireContentTypeMiddleware("application/json", func(ctx *rawhttp.Ctx) {
		ctx.SetBodyString("ok")
	})
	srv := &rawhttp.Server{Handler: h, ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1}
	fc := newFakeConn([]byte(
		"POST / HTTP/1.1\r\nHost: localhost\r\nContent-Type: text/plain\r\nContent-Length: 1\r\nConnection: close\r\n\r\nx",
	))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "415") {
		t.Fatalf("want 415: %q", fc.w.String())
	}
	fc = newFakeConn([]byte(
		"POST / HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/json; charset=utf-8\r\nContent-Length: 2\r\nConnection: close\r\n\r\n{}",
	))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "ok") {
		t.Fatalf("want ok: %q", fc.w.String())
	}
}

func TestRequireMethodsMiddleware(t *testing.T) {
	h := rawhttp.RequireMethodsMiddleware([]string{"GET", "HEAD"}, func(ctx *rawhttp.Ctx) {
		ctx.SetBodyString("ok")
	})
	srv := &rawhttp.Server{Handler: h, ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1}
	fc := newFakeConn([]byte("POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"))
	_ = srv.ServeConn(fc)
	resp := fc.w.String()
	if !strings.Contains(resp, "405") || !strings.Contains(resp, "Allow: GET, HEAD") {
		t.Fatalf("want 405+Allow: %q", resp)
	}
	fc = newFakeConn([]byte("GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "ok") {
		t.Fatalf("want ok: %q", fc.w.String())
	}
}

func TestHTTPSRedirectMiddleware(t *testing.T) {
	h := rawhttp.HTTPSRedirectMiddleware(func(ctx *rawhttp.Ctx) {
		ctx.SetBodyString("tls")
	})
	srv := &rawhttp.Server{Handler: h, ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1}
	fc := newFakeConn([]byte("GET /path?q=1 HTTP/1.1\r\nHost: ex.test\r\nConnection: close\r\n\r\n"))
	_ = srv.ServeConn(fc)
	resp := fc.w.String()
	if !strings.Contains(resp, "308") || !strings.Contains(resp, "Location: https://ex.test/path?q=1") {
		t.Fatalf("want 308 redirect: %q", resp)
	}
}
