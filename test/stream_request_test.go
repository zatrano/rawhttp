package test_test

import (
	"io"
	"strconv"
	"strings"
	"testing"

	"github.com/zatrano/rawhttp"
)

func TestStreamRequestBody_ContentLength(t *testing.T) {
	var got string
	srv := &rawhttp.Server{
		StreamRequestBody: true,
		Handler: func(ctx *rawhttp.Ctx) {
			r := ctx.RequestBodyStream()
			if r == nil {
				t.Error("nil stream")
				return
			}
			b, err := io.ReadAll(r)
			if err != nil {
				t.Errorf("read: %v", err)
				return
			}
			got = string(b)
			ctx.SetBodyString("ok")
		},
		ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
	}
	body := "streamed-payload-data"
	req := "POST /up HTTP/1.1\r\nHost: localhost\r\nContent-Length: " +
		strconv.Itoa(len(body)) + "\r\nConnection: close\r\n\r\n" + body
	fc := newFakeConn([]byte(req))
	if err := srv.ServeConn(fc); err != nil {
		t.Fatal(err)
	}
	if got != body {
		t.Fatalf("body=%q want %q", got, body)
	}
	if !strings.Contains(fc.w.String(), "ok") {
		t.Fatalf("resp=%q", fc.w.String())
	}
}

func TestStreamRequestBody_Chunked(t *testing.T) {
	var got string
	srv := &rawhttp.Server{
		StreamRequestBody: true,
		Handler: func(ctx *rawhttp.Ctx) {
			b, err := io.ReadAll(ctx.RequestBodyStream())
			if err != nil {
				t.Errorf("read: %v", err)
				return
			}
			got = string(b)
			ctx.SetBodyString("chunked-ok")
		},
		ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
	}
	req := "POST /c HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n" +
		"5\r\nhello\r\n6\r\n world\r\n0\r\n\r\n"
	fc := newFakeConn([]byte(req))
	if err := srv.ServeConn(fc); err != nil {
		t.Fatal(err)
	}
	if got != "hello world" {
		t.Fatalf("body=%q", got)
	}
}

func TestStreamRequestBody_DrainUnread(t *testing.T) {
	n := 0
	srv := &rawhttp.Server{
		StreamRequestBody: true,
		Handler: func(ctx *rawhttp.Ctx) {
			n++
			// Intentionally leave body unread — server must drain for keep-alive.
			ctx.SetBodyString("pong")
		},
		ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
	}
	req := "POST /a HTTP/1.1\r\nHost: localhost\r\nContent-Length: 4\r\n\r\nabcd" +
		"POST /b HTTP/1.1\r\nHost: localhost\r\nContent-Length: 4\r\nConnection: close\r\n\r\nefgh"
	fc := newFakeConn([]byte(req))
	_ = srv.ServeConn(fc)
	if n != 2 {
		t.Fatalf("handled %d want 2; resp=%q", n, fc.w.String())
	}
}

func TestStreamRequestBody_ExceedMax(t *testing.T) {
	srv := &rawhttp.Server{
		StreamRequestBody:  true,
		MaxRequestBodySize: 8,
		Handler:            func(ctx *rawhttp.Ctx) { ctx.SetBodyString("should-not") },
		ReadTimeout:        -1, WriteTimeout: -1, IdleTimeout: -1,
	}
	req := "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 20\r\nConnection: close\r\n\r\n" +
		"01234567890123456789"
	fc := newFakeConn([]byte(req))
	err := srv.ServeConn(fc)
	if err != rawhttp.ErrBodyTooLarge {
		t.Fatalf("err=%v", err)
	}
	if !strings.Contains(fc.w.String(), "413") {
		t.Fatalf("resp=%q", fc.w.String())
	}
}

func TestStreamRequestBody_OffByDefault(t *testing.T) {
	var bodyLen int
	srv := &rawhttp.Server{
		Handler: func(ctx *rawhttp.Ctx) {
			bodyLen = len(ctx.Body())
			ctx.SetBody(ctx.Body())
		},
		ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
	}
	req := "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 3\r\nConnection: close\r\n\r\nxyz"
	fc := newFakeConn([]byte(req))
	_ = srv.ServeConn(fc)
	if bodyLen != 3 {
		t.Fatalf("buffered body len=%d", bodyLen)
	}
}
