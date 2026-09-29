package test_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/zatrano/rawhttp"
)

func TestParseSimpleGET(t *testing.T) {
	req := "GET /hello HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
	var gotMethod, gotPath string
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) {
		gotMethod = string(ctx.Method)
		gotPath = string(ctx.Path)
		ctx.WriteString("world")
	}}
	fc := newFakeConn([]byte(req))
	if err := srv.ServeConn(fc); err != nil {
		t.Fatalf("ServeConn error: %v", err)
	}
	if gotMethod != "GET" || gotPath != "/hello" {
		t.Fatalf("got method=%q path=%q", gotMethod, gotPath)
	}
	resp := fc.w.String()
	if !strings.HasPrefix(resp, "HTTP/1.1 200 OK\r\n") {
		t.Fatalf("bad status line: %q", resp)
	}
	if !strings.Contains(resp, "Content-Length: 5\r\n") {
		t.Fatalf("bad content-length: %q", resp)
	}
	if !strings.HasSuffix(resp, "world") {
		t.Fatalf("bad body: %q", resp)
	}
}

func TestHeaderLookup(t *testing.T) {
	req := "GET / HTTP/1.1\r\nHost: localhost\r\nX-Custom: hello-world\r\nConnection: close\r\n\r\n"
	var got string
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) {
		got = string(ctx.Header("x-custom"))
	}}
	fc := newFakeConn([]byte(req))
	if err := srv.ServeConn(fc); err != nil {
		t.Fatal(err)
	}
	if got != "hello-world" {
		t.Fatalf("got %q", got)
	}
}

func TestContentLengthBodyPipelined(t *testing.T) {
	req := "POST /a HTTP/1.1\r\nHost: localhost\r\nContent-Length: 5\r\n\r\nhello" +
		"GET /b HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
	var paths []string
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) {
		paths = append(paths, string(ctx.Path))
	}}
	fc := newFakeConn([]byte(req))
	if err := srv.ServeConn(fc); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 || paths[0] != "/a" || paths[1] != "/b" {
		t.Fatalf("got %v", paths)
	}
}

func TestKeepAliveMultipleRequests(t *testing.T) {
	req := strings.Repeat("GET /ping HTTP/1.1\r\nHost: x\r\n\r\n", 50)
	count := 0
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) {
		count++
		ctx.WriteString("pong")
	}}
	fc := newFakeConn([]byte(req))
	err := srv.ServeConn(fc)
	if err == nil {
		t.Fatal("expected EOF-at-boundary error")
	}
	if count != 50 {
		t.Fatalf("expected 50, got %d", count)
	}
	if strings.Count(fc.w.String(), "pong") != 50 {
		t.Fatal("expected 50 responses")
	}
}

func TestHTTP10DefaultsToClose(t *testing.T) {
	req := "GET / HTTP/1.0\r\n\r\n"
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) { ctx.WriteString("ok") }}
	fc := newFakeConn([]byte(req))
	if err := srv.ServeConn(fc); err != nil {
		t.Fatal(err)
	}
}

func TestFragmentedReadsDoNotCorruptHeaders(t *testing.T) {
	req := "GET /path HTTP/1.1\r\n" +
		"Host: example.com\r\n" +
		"X-First: alpha-value\r\n" +
		"X-Second: bravo-value\r\n" +
		"X-Third: charlie-value\r\n" +
		"Connection: close\r\n" +
		"\r\n"
	for _, chunkSize := range []int{1, 2, 3, 5, 7} {
		cs := chunkSize
		t.Run("chunk_"+strconv.Itoa(cs), func(t *testing.T) {
			var gotPath, gotFirst, gotSecond, gotThird string
			srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) {
				gotPath = string(ctx.Path)
				gotFirst = string(ctx.Header("X-First"))
				gotSecond = string(ctx.Header("X-Second"))
				gotThird = string(ctx.Header("X-Third"))
			}}
			cc := newChunkedConn([]byte(req), cs)
			if err := srv.ServeConn(cc); err != nil {
				t.Fatal(err)
			}
			if gotPath != "/path" {
				t.Fatalf("path %q", gotPath)
			}
			if gotFirst != "alpha-value" || gotSecond != "bravo-value" || gotThird != "charlie-value" {
				t.Fatalf("headers corrupted: %q %q %q", gotFirst, gotSecond, gotThird)
			}
		})
	}
}

func TestFragmentedReadsWithBody(t *testing.T) {
	req := "POST /a HTTP/1.1\r\n" +
		"Host: localhost\r\n" +
		"X-Tag: keep-me\r\n" +
		"Content-Length: 11\r\n" +
		"\r\n" +
		"hello world" +
		"GET /b HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
	var tags, paths []string
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) {
		paths = append(paths, string(ctx.Path))
		tags = append(tags, string(ctx.Header("X-Tag")))
	}}
	cc := newChunkedConn([]byte(req), 4)
	if err := srv.ServeConn(cc); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 || paths[0] != "/a" || paths[1] != "/b" {
		t.Fatalf("paths %v", paths)
	}
	if tags[0] != "keep-me" {
		t.Fatalf("header corrupted: %q", tags[0])
	}
}
