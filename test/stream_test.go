package test_test

import (
	"bufio"
	"strings"
	"testing"

	"github.com/zatrano/rawhttp"
)

func TestSetBodyStreamWriter(t *testing.T) {
	srv := &rawhttp.Server{
		Handler: func(ctx *rawhttp.Ctx) {
			ctx.SetBodyStreamWriter(func(w *bufio.Writer) {
				_, _ = w.WriteString("hello")
				_ = w.Flush()
				_, _ = w.WriteString("-world")
			})
		},
		ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
	}
	fc := newFakeConn([]byte("GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"))
	_ = srv.ServeConn(fc)
	resp := fc.w.String()
	if !strings.Contains(resp, "Transfer-Encoding: chunked") {
		t.Fatalf("want chunked: %q", resp)
	}
	if !strings.Contains(resp, "hello") || !strings.Contains(resp, "world") {
		t.Fatalf("want streamed body: %q", resp)
	}
}

func TestWriteBufferSize_StreamResponse(t *testing.T) {
	srv := &rawhttp.Server{
		WriteBufferSize: 1024,
		Handler: func(ctx *rawhttp.Ctx) {
			ctx.SetBodyStreamWriter(func(w *bufio.Writer) {
				_, _ = w.WriteString("buffered-stream")
			})
		},
		ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
	}
	fc := newFakeConn([]byte("GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "buffered-stream") {
		t.Fatalf("resp=%q", fc.w.String())
	}
}
