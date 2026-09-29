package test_test

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/zatrano/rawhttp"
)

func TestBasicAuth(t *testing.T) {
	cred := base64.StdEncoding.EncodeToString([]byte("alice:s3cret"))
	req := "GET / HTTP/1.1\r\nHost: localhost\r\nAuthorization: Basic " + cred + "\r\nConnection: close\r\n\r\n"
	var user, pass string
	var ok bool
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) {
		user, pass, ok = ctx.BasicAuth()
		if !ok || user != "alice" {
			ctx.SetBasicAuthChallenge("api")
			return
		}
		ctx.SetBody([]byte("hi"))
	}, ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1}
	fc := newFakeConn([]byte(req))
	_ = srv.ServeConn(fc)
	if !ok || user != "alice" || pass != "s3cret" {
		t.Fatalf("auth=%v user=%q pass=%q", ok, user, pass)
	}
	if !strings.Contains(fc.w.String(), "hi") {
		t.Fatalf("body: %q", fc.w.String())
	}

	fc = newFakeConn([]byte("GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"))
	_ = srv.ServeConn(fc)
	resp := fc.w.String()
	if !strings.Contains(resp, "401") || !strings.Contains(resp, `WWW-Authenticate: Basic realm="api"`) {
		t.Fatalf("want challenge: %q", resp)
	}
}

func TestAllowedHostsAndMaxURI(t *testing.T) {
	srv := &rawhttp.Server{
		AllowedHosts: []string{"app.local"},
		MaxURILength: 16,
		Handler:      func(ctx *rawhttp.Ctx) { ctx.SetBody([]byte("ok")) },
		ReadTimeout:  -1, WriteTimeout: -1, IdleTimeout: -1,
	}
	fc := newFakeConn([]byte("GET / HTTP/1.1\r\nHost: evil.test\r\nConnection: close\r\n\r\n"))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "421") {
		t.Fatalf("want 421: %q", fc.w.String())
	}

	fc = newFakeConn([]byte("GET / HTTP/1.1\r\nHost: app.local\r\nConnection: close\r\n\r\n"))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "ok") {
		t.Fatalf("allowed host: %q", fc.w.String())
	}

	long := "/" + strings.Repeat("a", 20)
	fc = newFakeConn([]byte("GET " + long + " HTTP/1.1\r\nHost: app.local\r\nConnection: close\r\n\r\n"))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "414") {
		t.Fatalf("want 414: %q", fc.w.String())
	}
}

func TestAllowedMethods(t *testing.T) {
	srv := &rawhttp.Server{
		AllowedMethods: []string{"GET", "HEAD"},
		Handler:        func(ctx *rawhttp.Ctx) { ctx.SetBody([]byte("ok")) },
		ReadTimeout:    -1, WriteTimeout: -1, IdleTimeout: -1,
	}
	fc := newFakeConn([]byte("POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"))
	_ = srv.ServeConn(fc)
	resp := fc.w.String()
	if !strings.Contains(resp, "405") || !strings.Contains(resp, "Allow: GET, HEAD") {
		t.Fatalf("want 405+Allow: %q", resp)
	}

	fc = newFakeConn([]byte("GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "ok") {
		t.Fatalf("GET allowed: %q", fc.w.String())
	}
}

func TestClientIPTrustedProxies(t *testing.T) {
	var got string
	srv := &rawhttp.Server{
		TrustedProxies: []string{"127.0.0.0/8"},
		Handler: func(ctx *rawhttp.Ctx) {
			got = ctx.ClientIP()
			ctx.SetBody([]byte(got))
		},
		ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
	}
	fc := newFakeConn([]byte(
		"GET / HTTP/1.1\r\nHost: localhost\r\nX-Forwarded-For: 203.0.113.9, 10.0.0.1\r\nConnection: close\r\n\r\n",
	))
	_ = srv.ServeConn(fc)
	if got != "203.0.113.9" {
		t.Fatalf("trusted peer ClientIP=%q want 203.0.113.9", got)
	}

	srv2 := &rawhttp.Server{
		TrustedProxies: []string{"10.0.0.0/8"},
		Handler: func(ctx *rawhttp.Ctx) {
			got = ctx.ClientIP()
			ctx.SetBody([]byte(got))
		},
		ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
	}
	fc = newFakeConn([]byte(
		"GET / HTTP/1.1\r\nHost: localhost\r\nX-Forwarded-For: 203.0.113.9\r\nConnection: close\r\n\r\n",
	))
	_ = srv2.ServeConn(fc)
	if got != "127.0.0.1" {
		t.Fatalf("untrusted peer must ignore XFF: got %q", got)
	}
}

func TestUserValue(t *testing.T) {
	var got any
	var visited int
	srv := &rawhttp.Server{
		Handler: func(ctx *rawhttp.Ctx) {
			ctx.SetUserValue("k", "v")
			got = ctx.UserValue("k")
			ctx.VisitUserValues(func(key, val any) {
				if key == "k" && val == "v" {
					visited++
				}
			})
			ctx.AppendBody([]byte("ok"))
		},
		ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
	}
	fc := newFakeConn([]byte("GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"))
	_ = srv.ServeConn(fc)
	if got != "v" || visited != 1 {
		t.Fatalf("UserValue=%v visited=%d", got, visited)
	}
	if !strings.Contains(fc.w.String(), "ok") {
		t.Fatalf("body: %q", fc.w.String())
	}
}

func TestContinueHandlerReject(t *testing.T) {
	srv := &rawhttp.Server{
		ContinueHandler: func(ctx *rawhttp.Ctx) bool { return false },
		Handler:         func(ctx *rawhttp.Ctx) { t.Fatal("handler should not run") },
		ReadTimeout:     -1, WriteTimeout: -1, IdleTimeout: -1,
	}
	fc := newFakeConn([]byte(
		"POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 5\r\nExpect: 100-continue\r\nConnection: close\r\n\r\nhello",
	))
	err := srv.ServeConn(fc)
	if err != nil && !strings.Contains(err.Error(), "expectation") {
		// errExpectationFailed is expected
	}
	resp := fc.w.String()
	if strings.Contains(resp, "100 Continue") {
		t.Fatalf("must not send 100: %q", resp)
	}
	if !strings.Contains(resp, "417") {
		t.Fatalf("want 417: %q", resp)
	}
}

func TestMaxConnsPerIP(t *testing.T) {
	hold := make(chan struct{})
	release := make(chan struct{})
	srv := &rawhttp.Server{
		MaxConnsPerIP: 1,
		Handler: func(ctx *rawhttp.Ctx) {
			close(hold)
			<-release
			ctx.SetBody([]byte("ok"))
		},
		ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
	}
	done := make(chan error, 1)
	go func() {
		fc := newFakeConn([]byte("GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"))
		done <- srv.ServeConn(fc)
	}()
	select {
	case <-hold:
	case <-time.After(2 * time.Second):
		t.Fatal("first conn did not enter handler")
	}
	fc2 := newFakeConn([]byte("GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"))
	err := srv.ServeConn(fc2)
	if err != rawhttp.ErrPerIPConnLimit {
		t.Fatalf("want ErrPerIPConnLimit, got %v", err)
	}
	if !strings.Contains(fc2.w.String(), "429") {
		t.Fatalf("want 429: %q", fc2.w.String())
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("first ServeConn: %v", err)
	}
}
