package test_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zatrano/rawhttp"
)

func TestSendFileAndNotModified(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(path, []byte("hello-file"), 0o644); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	mod := fi.ModTime().UTC().Truncate(time.Second)

	srv := &rawhttp.Server{
		Handler: func(ctx *rawhttp.Ctx) {
			ctx.SendFile(path)
		},
		ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
	}
	fc := newFakeConn([]byte("GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "hello-file") {
		t.Fatalf("want body: %q", fc.w.String())
	}

	req := "GET / HTTP/1.1\r\nHost: localhost\r\nIf-Modified-Since: " + mod.Format("Mon, 02 Jan 2006 15:04:05 GMT") + "\r\nConnection: close\r\n\r\n"
	fc = newFakeConn([]byte(req))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "304") {
		t.Fatalf("want 304: %q", fc.w.String())
	}
}

func TestSuccessAndConnMeta(t *testing.T) {
	var id, connID, reqN uint64
	var gotTime bool
	srv := &rawhttp.Server{
		Handler: func(ctx *rawhttp.Ctx) {
			ctx.Success("text/plain", []byte("ok"))
			id = ctx.ID()
			connID = ctx.ConnID()
			reqN = ctx.ConnRequestNum()
			gotTime = !ctx.Time().IsZero() && !ctx.ConnTime().IsZero()
			if ctx.Conn() == nil {
				t.Error("Conn nil")
			}
		},
		ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
	}
	fc := newFakeConn([]byte(
		"GET / HTTP/1.1\r\nHost: localhost\r\n\r\n" +
			"GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n",
	))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "ok") {
		t.Fatalf("body: %q", fc.w.String())
	}
	if connID == 0 || reqN != 2 || id == 0 || !gotTime {
		t.Fatalf("connID=%d reqN=%d id=%d time=%v", connID, reqN, id, gotTime)
	}
}

func TestRetryIfErr(t *testing.T) {
	attempts := 0
	c := &rawhttp.Client{
		MaxIdemponentCallAttempts: 3,
		RetryIfErr: func(req *rawhttp.Request, n int, err error) (bool, bool) {
			attempts = n
			return false, n < 2 // retry once
		},
		DialTimeout: 50 * time.Millisecond,
	}
	resp := rawhttp.AcquireResponse()
	defer rawhttp.ReleaseResponse(resp)
	_ = c.Get("http://127.0.0.1:1/", resp) // connection refused
	if attempts < 1 {
		t.Fatalf("RetryIfErr not called, attempts=%d", attempts)
	}
}
