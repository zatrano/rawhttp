// Comparison benchmarks: rawhttp vs fasthttp vs net/http.
//
//	cd test && go test -bench=. -benchmem
package test_test

import (
	"bytes"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/valyala/fasthttp"
	"github.com/zatrano/rawhttp"
)

var plaintextBody = []byte("Hello, World!")

const oneRequest = "GET / HTTP/1.1\r\nHost: localhost\r\nUser-Agent: bench\r\nAccept: */*\r\n\r\n"

func buildRequests(n int) []byte {
	var b bytes.Buffer
	b.Grow(len(oneRequest) * n)
	for i := 0; i < n; i++ {
		b.WriteString(oneRequest)
	}
	return b.Bytes()
}

type oneConnListener struct {
	conn net.Conn
	used bool
}

var errListenerDone = errors.New("benchmark: listener exhausted")

func (l *oneConnListener) Accept() (net.Conn, error) {
	if l.used {
		return nil, errListenerDone
	}
	l.used = true
	return l.conn, nil
}
func (l *oneConnListener) Close() error   { return nil }
func (l *oneConnListener) Addr() net.Addr { return dummyAddr{} }

func BenchmarkRawHTTP_Plaintext(b *testing.B) {
	skipIfPoison(b)
	data := buildRequests(b.N)
	fc := newDiscardConn(data)
	srv := &rawhttp.Server{
		ReadTimeout:  -1,
		WriteTimeout: -1,
		IdleTimeout:  -1,
		Handler:      func(ctx *rawhttp.Ctx) { ctx.SetBody(plaintextBody) },
	}
	b.ReportAllocs()
	b.ResetTimer()
	_ = srv.ServeConn(fc)
}

func BenchmarkFastHTTP_Plaintext(b *testing.B) {
	skipIfPoison(b)
	data := buildRequests(b.N)
	fc := newDiscardConn(data)
	srv := &fasthttp.Server{
		Handler: func(ctx *fasthttp.RequestCtx) {
			ctx.SetBody(plaintextBody)
		},
	}
	b.ReportAllocs()
	b.ResetTimer()
	_ = srv.ServeConn(fc)
}

func BenchmarkNetHTTP_Plaintext(b *testing.B) {
	skipIfPoison(b)
	data := buildRequests(b.N)
	fc := newDiscardConn(data)
	ln := &oneConnListener{conn: fc}
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(plaintextBody)
		}),
	}
	b.ReportAllocs()
	b.ResetTimer()
	_ = srv.Serve(ln)
	select {
	case <-fc.doneCh:
	case <-time.After(30 * time.Second):
		b.Fatal("timed out waiting for net/http")
	}
}

func BenchmarkRawHTTP_JSONPost(b *testing.B) {
	skipIfPoison(b)
	payload := []byte(`{"msg":"hello"}`)
	one := "POST /api HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/json\r\nContent-Length: 16\r\n\r\n" + string(payload)
	var buf bytes.Buffer
	buf.Grow(len(one) * b.N)
	for i := 0; i < b.N; i++ {
		buf.WriteString(one)
	}
	fc := newDiscardConn(buf.Bytes())
	srv := &rawhttp.Server{
		ReadTimeout:  -1,
		WriteTimeout: -1,
		IdleTimeout:  -1,
		Handler: func(ctx *rawhttp.Ctx) {
			ctx.SetContentType("application/json")
			_ = ctx.SetHeader("X-Powered-By", "rawhttp")
			ctx.SetBody(ctx.Body())
		},
	}
	b.ReportAllocs()
	b.ResetTimer()
	_ = srv.ServeConn(fc)
}

func BenchmarkFastHTTP_JSONPost(b *testing.B) {
	skipIfPoison(b)
	payload := []byte(`{"msg":"hello"}`)
	one := "POST /api HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/json\r\nContent-Length: 16\r\n\r\n" + string(payload)
	var buf bytes.Buffer
	buf.Grow(len(one) * b.N)
	for i := 0; i < b.N; i++ {
		buf.WriteString(one)
	}
	fc := newDiscardConn(buf.Bytes())
	srv := &fasthttp.Server{
		Handler: func(ctx *fasthttp.RequestCtx) {
			ctx.SetContentType("application/json")
			ctx.Response.Header.Set("X-Powered-By", "rawhttp")
			ctx.SetBody(ctx.PostBody())
		},
	}
	b.ReportAllocs()
	b.ResetTimer()
	_ = srv.ServeConn(fc)
}

func BenchmarkRawHTTP_HeaderPeek(b *testing.B) {
	skipIfPoison(b)
	data := buildRequests(b.N)
	fc := newDiscardConn(data)
	srv := &rawhttp.Server{
		ReadTimeout:  -1,
		WriteTimeout: -1,
		IdleTimeout:  -1,
		Handler: func(ctx *rawhttp.Ctx) {
			_ = ctx.Header("User-Agent")
			_ = ctx.Header("Accept")
			_ = ctx.Host()
			ctx.SetBody(plaintextBody)
		},
	}
	b.ReportAllocs()
	b.ResetTimer()
	_ = srv.ServeConn(fc)
}

func BenchmarkRawHTTP_ChunkedEcho(b *testing.B) {
	skipIfPoison(b)
	one := "POST /c HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nhello\r\n0\r\n\r\n"
	var buf bytes.Buffer
	buf.Grow(len(one) * b.N)
	for i := 0; i < b.N; i++ {
		buf.WriteString(one)
	}
	fc := newDiscardConn(buf.Bytes())
	srv := &rawhttp.Server{
		ReadTimeout:  -1,
		WriteTimeout: -1,
		IdleTimeout:  -1,
		Handler: func(ctx *rawhttp.Ctx) {
			ctx.SetChunked()
			ctx.Write(ctx.Body())
		},
	}
	b.ReportAllocs()
	b.ResetTimer()
	_ = srv.ServeConn(fc)
}
