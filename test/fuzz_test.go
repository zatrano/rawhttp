package test_test

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/zatrano/rawhttp"
)

// FuzzServeConn feeds arbitrary bytes to ServeConn.
// Contract: never panic; malformed input must not crash the process.
func FuzzServeConn(f *testing.F) {
	seeds := []string{
		"GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n",
		"POST /echo HTTP/1.1\r\nHost: localhost\r\nContent-Length: 5\r\nConnection: close\r\n\r\nhello",
		"POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n5\r\nhello\r\n0\r\n\r\n",
		"HEAD / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n",
		"OPTIONS * HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n",
		"GET /a?b=1&c=2 HTTP/1.1\r\nHost: localhost\r\nUser-Agent: fuzz\r\nConnection: close\r\n\r\n",
		// Attack seeds
		"POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 4\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n0\r\n\r\n",
		"GET / HTTP/1.1\r\nHost: a\r\nHost: b\r\nConnection: close\r\n\r\n",
		"GET /../x HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n",
		"GET / HTTP/1.1\nHost: localhost\n\n",
		"TRACE / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n",
		"GET http://evil/ HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n",
		"POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\nFFFFFFFF\r\n",
		"POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n5;ext=\x01\r\nhello\r\n0\r\n\r\n",
		"POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n0\r\nContent-Length: 5\r\n\r\n",
		"",
		"\r\n\r\n",
		"GET /",
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 64<<10 {
			t.Skip()
		}
		srv := &rawhttp.Server{
			Handler: func(ctx *rawhttp.Ctx) {
				_ = ctx.Method
				_ = ctx.Path
				_ = ctx.Query
				_ = ctx.Host()
				_ = ctx.Body()
				_ = ctx.Header("User-Agent")
				ctx.VisitHeader(func(_, _ []byte) {})
				ctx.SetBody([]byte("ok"))
			},
			ReadTimeout:        100 * time.Millisecond,
			WriteTimeout:       100 * time.Millisecond,
			IdleTimeout:        100 * time.Millisecond,
			MaxRequestBodySize: 64 << 10,
			MaxHeaderBytes:     16 << 10,
		}
		fc := newFakeConn(data)
		_ = srv.ServeConn(fc)
		_ = fc.w.String() // ensure response buffer is touched
	})
}

// FuzzRequestLine mutates only the request-line while keeping a valid header block.
func FuzzRequestLine(f *testing.F) {
	for _, line := range []string{
		"GET / HTTP/1.1",
		"POST /echo HTTP/1.1",
		"HEAD /x HTTP/1.0",
		"OPTIONS * HTTP/1.1",
		"GET /a?b=1 HTTP/1.1",
		"GET /../x HTTP/1.1",
		"GET http://h/ HTTP/1.1",
		"TRACE / HTTP/1.1",
		"GET / HTTP/2.0",
		"GET / HTTP/1.1\x00",
	} {
		f.Add(line)
	}

	f.Fuzz(func(t *testing.T, line string) {
		if len(line) > 4096 {
			t.Skip()
		}
		req := line + "\r\nHost: localhost\r\nConnection: close\r\n\r\n"
		srv := &rawhttp.Server{
			Handler:      func(ctx *rawhttp.Ctx) { ctx.SetBody([]byte("ok")) },
			ReadTimeout:  100 * time.Millisecond,
			WriteTimeout: 100 * time.Millisecond,
			IdleTimeout:  100 * time.Millisecond,
		}
		_ = srv.ServeConn(newFakeConn([]byte(req)))
	})
}

// FuzzHeaders mutates a single extra header line.
func FuzzHeaders(f *testing.F) {
	for _, h := range []string{
		"X-A: 1",
		"Content-Length: 0",
		"Transfer-Encoding: chunked",
		"Host: other",
		"Connection: close",
		"X-Bad: a\x01b",
		"Bad Name: x",
		"X-A: 1\r\n folded",
		"Content-Length: 05",
		"Expect: 100-continue",
		"Upgrade: websocket",
		"Cookie: a=1; b=2",
	} {
		f.Add(h)
	}

	f.Fuzz(func(t *testing.T, hdr string) {
		if len(hdr) > 8192 {
			t.Skip()
		}
		req := "GET / HTTP/1.1\r\nHost: localhost\r\n" + hdr + "\r\nConnection: close\r\n\r\n"
		// If fuzz injects chunked TE without body, append a terminating chunk.
		if strings.Contains(strings.ToLower(hdr), "transfer-encoding") &&
			strings.Contains(strings.ToLower(hdr), "chunked") {
			req = "POST / HTTP/1.1\r\nHost: localhost\r\n" + hdr + "\r\nConnection: close\r\n\r\n0\r\n\r\n"
		}
		srv := &rawhttp.Server{
			Handler:      func(ctx *rawhttp.Ctx) { ctx.SetBody([]byte("ok")) },
			ReadTimeout:  100 * time.Millisecond,
			WriteTimeout: 100 * time.Millisecond,
			IdleTimeout:  100 * time.Millisecond,
			MaxHeaders:   50,
		}
		_ = srv.ServeConn(newFakeConn([]byte(req)))
	})
}

// FuzzChunkedBody mutates the chunked body after a fixed valid header.
func FuzzChunkedBody(f *testing.F) {
	for _, body := range []string{
		"0\r\n\r\n",
		"5\r\nhello\r\n0\r\n\r\n",
		"5\r\nhello\r\n3\r\nbye\r\n0\r\n\r\n",
		"FFFFFFFF\r\n",
		"5;foo=bar\r\nhello\r\n0\r\n\r\n",
		"0\r\nX-Trailer: v\r\n\r\n",
		"0\r\nContent-Length: 1\r\n\r\n",
		"g\r\nxx\r\n0\r\n\r\n",
		"5\r\nhell",
		"",
	} {
		f.Add([]byte(body))
	}

	f.Fuzz(func(t *testing.T, body []byte) {
		if len(body) > 32<<10 {
			t.Skip()
		}
		req := append([]byte("POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n"), body...)
		srv := &rawhttp.Server{
			Handler: func(ctx *rawhttp.Ctx) {
				_ = ctx.Body()
				ctx.SetBody([]byte("ok"))
			},
			ReadTimeout:        100 * time.Millisecond,
			WriteTimeout:       100 * time.Millisecond,
			IdleTimeout:        100 * time.Millisecond,
			MaxRequestBodySize: 32 << 10,
			MaxChunks:          256,
		}
		_ = srv.ServeConn(newFakeConn(req))
	})
}

// FuzzClientResponse feeds arbitrary response bytes to HostClient.
// Contract: never panic.
func FuzzClientResponse(f *testing.F) {
	for _, s := range []string{
		"HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nok",
		"HTTP/1.1 200 OK\r\nC: x\r\nContent-Length: 0\r\nConnection: close\r\n\r\n",
		"HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\nContent-Length: 1\r\nConnection: close\r\n\r\n0\r\n\r\n",
		"HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n5\r\nhello\r\n0\r\nContent-Length: 1\r\n\r\n",
		"HTTP/1.1 204 No Content\r\nConnection: close\r\n\r\n",
		"HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n0\r\nX-A: 1\r\nX-B: 2\r\n\r\n",
		"HTTP/1.0 200 OK\r\n\r\nok",
		"",
		"HTTP/1.1 200 OK\r\n",
	} {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 16<<10 {
			t.Skip()
		}
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()
		go func() {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			defer conn.Close()
			buf := make([]byte, 4096)
			_ = conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
			_, _ = conn.Read(buf)
			_ = conn.SetWriteDeadline(time.Now().Add(200 * time.Millisecond))
			_, _ = conn.Write(data)
		}()

		hc := &rawhttp.HostClient{
			Addr:        ln.Addr().String(),
			ReadTimeout: 300 * time.Millisecond,
			DialTimeout: 300 * time.Millisecond,
		}
		defer hc.CloseIdleConnections()
		req := rawhttp.AcquireRequest()
		resp := rawhttp.AcquireResponse()
		defer rawhttp.ReleaseRequest(req)
		defer rawhttp.ReleaseResponse(resp)
		req.RequestURI = "/"
		_ = hc.Do(req, resp) // must not panic
	})
}
