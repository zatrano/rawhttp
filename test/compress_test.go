package test_test

import (
	"bytes"
	"compress/gzip"
	"io"
	"strings"
	"testing"

	"github.com/zatrano/rawhttp"
)

func TestCompress_GzipMiddleware(t *testing.T) {
	payload := bytes.Repeat([]byte("abcdefghij"), 40) // 400 bytes > min
	h := rawhttp.CompressMiddleware(func(ctx *rawhttp.Ctx) {
		ctx.SetContentType("text/plain")
		ctx.SetBody(payload)
	}, rawhttp.CompressOptions{MinLength: 100})

	req := "GET / HTTP/1.1\r\nHost: localhost\r\nAccept-Encoding: gzip\r\nConnection: close\r\n\r\n"
	srv := &rawhttp.Server{Handler: h}
	fc := newFakeConn([]byte(req))
	if err := srv.ServeConn(fc); err != nil {
		t.Fatal(err)
	}
	resp := fc.w.Bytes()
	if !bytes.Contains(resp, []byte("Content-Encoding: gzip\r\n")) {
		t.Fatalf("missing gzip encoding: %s", resp)
	}
	idx := bytes.Index(resp, []byte("\r\n\r\n"))
	if idx < 0 {
		t.Fatal("no body")
	}
	gr, err := gzip.NewReader(bytes.NewReader(resp[idx+4:]))
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(gr)
	_ = gr.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("decoded len=%d", len(got))
	}
}

func TestCompress_NoCompressWithoutAccept(t *testing.T) {
	payload := bytes.Repeat([]byte("x"), 400)
	h := rawhttp.CompressMiddleware(func(ctx *rawhttp.Ctx) {
		ctx.SetBody(payload)
	}, rawhttp.CompressOptions{MinLength: 100})
	req := "GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
	srv := &rawhttp.Server{Handler: h}
	fc := newFakeConn([]byte(req))
	_ = srv.ServeConn(fc)
	if strings.Contains(fc.w.String(), "Content-Encoding:") {
		t.Fatalf("unexpected encoding: %q", fc.w.String())
	}
}

func TestCompress_ContentTypesFilter(t *testing.T) {
	payload := bytes.Repeat([]byte("abcdefghij"), 40)
	req := "GET / HTTP/1.1\r\nHost: localhost\r\nAccept-Encoding: gzip\r\nConnection: close\r\n\r\n"

	h := rawhttp.CompressMiddleware(func(ctx *rawhttp.Ctx) {
		ctx.SetContentType("image/png")
		ctx.SetBody(payload)
	}, rawhttp.CompressOptions{MinLength: 100, ContentTypes: []string{"text/", "application/json"}})
	srv := &rawhttp.Server{Handler: h, ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1}
	fc := newFakeConn([]byte(req))
	_ = srv.ServeConn(fc)
	if strings.Contains(fc.w.String(), "Content-Encoding:") {
		t.Fatalf("image/png must not compress: %q", fc.w.String())
	}

	h2 := rawhttp.CompressMiddleware(func(ctx *rawhttp.Ctx) {
		ctx.SetContentType("text/html; charset=utf-8")
		ctx.SetBody(payload)
	}, rawhttp.CompressOptions{MinLength: 100, ContentTypes: []string{"text/"}})
	srv2 := &rawhttp.Server{Handler: h2, ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1}
	fc = newFakeConn([]byte(req))
	_ = srv2.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "Content-Encoding: gzip") {
		t.Fatalf("text/html should compress: %q", fc.w.String())
	}
}
