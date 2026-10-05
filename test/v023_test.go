package test_test

import (
	"io"
	"net"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zatrano/rawhttp"
)

func TestExpectLargeContentLengthSkipsContinue(t *testing.T) {
	var hit int
	srv := &rawhttp.Server{
		ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
		MaxRequestBodySize: 32,
		Handler:            func(*rawhttp.Ctx) { hit++ },
	}
	req := "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 100\r\nExpect: 100-continue\r\nConnection: close\r\n\r\n"
	fc := newFakeConn([]byte(req))
	err := srv.ServeConn(fc)
	if err != rawhttp.ErrBodyTooLarge {
		t.Fatalf("err=%v", err)
	}
	out := fc.w.String()
	if strings.Contains(out, "100 Continue") {
		t.Fatalf("wrote 100 Continue: %q", out)
	}
	if !strings.Contains(out, "413") || !strings.Contains(out, "Connection: close") {
		t.Fatalf("resp=%q", out)
	}
	if hit != 0 {
		t.Fatalf("handler ran %d times", hit)
	}
}

func TestExpectChunkedSendsContinueThenLimits(t *testing.T) {
	var got string
	srv := &rawhttp.Server{
		ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
		MaxRequestBodySize: 4,
		Handler: func(ctx *rawhttp.Ctx) {
			got = string(ctx.Body())
		},
	}
	req := "POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\nExpect: 100-continue\r\nConnection: close\r\n\r\n" +
		"3\r\nabc\r\n0\r\n\r\n"
	fc := newFakeConn([]byte(req))
	if err := srv.ServeConn(fc); err != nil {
		t.Fatal(err)
	}
	out := fc.w.String()
	if !strings.Contains(out, "100 Continue") {
		t.Fatalf("missing 100: %q", out)
	}
	if got != "abc" {
		t.Fatalf("body=%q", got)
	}
}

func TestExpectChunkedOverLimitStillSendsContinue(t *testing.T) {
	var hit int
	srv := &rawhttp.Server{
		ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
		MaxRequestBodySize: 4,
		Handler:            func(*rawhttp.Ctx) { hit++ },
	}
	req := "POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\nExpect: 100-continue\r\nConnection: close\r\n\r\n" +
		"8\r\nabcdefgh\r\n0\r\n\r\n"
	fc := newFakeConn([]byte(req))
	err := srv.ServeConn(fc)
	out := fc.w.String()
	if !strings.Contains(out, "100 Continue") {
		t.Fatalf("missing 100: %q err=%v", out, err)
	}
	if err != rawhttp.ErrBodyTooLarge || !strings.Contains(out, "413") {
		t.Fatalf("err=%v resp=%q", err, out)
	}
	if hit != 0 {
		t.Fatalf("handler ran")
	}
}

func TestChunkedBodyPastReadBufferReturns413(t *testing.T) {
	var hit int
	srv := &rawhttp.Server{
		ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
		ReadBufferSize:     256,
		MaxRequestBodySize: 1024,
		Handler:            func(*rawhttp.Ctx) { hit++ },
	}
	payload := strings.Repeat("z", 2048)
	req := "POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n" +
		"800\r\n" + payload + "\r\n0\r\n\r\n"
	fc := newFakeConn([]byte(req))
	err := srv.ServeConn(fc)
	out := fc.w.String()
	if err != rawhttp.ErrBodyTooLarge || !strings.Contains(out, "413") {
		t.Fatalf("err=%v resp=%q", err, out)
	}
	if strings.Contains(out, "431") || hit != 0 {
		t.Fatalf("hit=%d resp=%q", hit, out)
	}
}

func TestRejectStatusSkipsHandlerAndPipeline(t *testing.T) {
	var hit int
	srv := &rawhttp.Server{
		ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
		HeaderReceived: func(*rawhttp.Ctx) rawhttp.RequestConfig {
			return rawhttp.RequestConfig{RejectStatus: 503, RejectRetryAfter: 1}
		},
		Handler: func(*rawhttp.Ctx) { hit++ },
	}
	req := "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 4\r\n\r\nBODY" +
		"GET /next HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
	fc := newFakeConn([]byte(req))
	err := srv.ServeConn(fc)
	if err != nil && !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("err=%v", err)
	}
	out := fc.w.String()
	if hit != 0 {
		t.Fatalf("handler ran")
	}
	if !strings.Contains(out, "503") || !strings.Contains(out, "Retry-After: 1") || !strings.Contains(out, "Connection: close") {
		t.Fatalf("resp=%q", out)
	}
	if strings.Contains(out, "GET /next") || strings.Count(out, "HTTP/1.1") != 1 {
		t.Fatalf("pipelined request was handled: %q", out)
	}
}

func TestStreamBodyDoesNotBufferAndClosesUnread(t *testing.T) {
	const n = 1 << 20
	var saw int
	srv := &rawhttp.Server{
		ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
		HeaderReceived: func(*rawhttp.Ctx) rawhttp.RequestConfig {
			return rawhttp.RequestConfig{StreamBody: true}
		},
		Handler: func(ctx *rawhttp.Ctx) {
			r := ctx.RequestBodyStream()
			if r == nil {
				t.Errorf("missing stream")
				return
			}
			buf := make([]byte, 32)
			k, err := r.Read(buf)
			saw = k
			if err != nil && err != io.EOF {
				t.Errorf("read: %v", err)
			}
			ctx.SetBodyString("ok")
		},
	}
	body := strings.Repeat("x", n)
	req := "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: " + itoa(n) + "\r\nConnection: close\r\n\r\n" + body
	fc := newFakeConn([]byte(req))
	err := srv.ServeConn(fc)
	if err == nil || !strings.Contains(err.Error(), "not consumed") {
		t.Fatalf("err=%v", err)
	}
	if saw == 0 || saw > 32 {
		t.Fatalf("read %d, want a small prefix", saw)
	}
}

func TestStreamBodyLargeReadStaysUnderMemoryCeiling(t *testing.T) {
	const n = 4 << 20
	srv := &rawhttp.Server{
		ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
		HeaderReceived: func(*rawhttp.Ctx) rawhttp.RequestConfig {
			return rawhttp.RequestConfig{StreamBody: true}
		},
		Handler: func(ctx *rawhttp.Ctx) {
			r := ctx.RequestBodyStream()
			if r == nil {
				t.Fatal("missing stream")
			}
			buf := make([]byte, 8<<10)
			var got int
			for {
				k, err := r.Read(buf)
				got += k
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatalf("read: %v", err)
				}
			}
			if got != n {
				t.Fatalf("got %d", got)
			}
			if len(ctx.Body()) != 0 {
				t.Fatalf("buffered %d bytes", len(ctx.Body()))
			}
			ctx.SetBodyString("ok")
		},
	}
	body := strings.Repeat("y", n)
	req := "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: " + itoa(n) + "\r\nConnection: close\r\n\r\n" + body
	fc := newFakeConn([]byte(req))
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	if err := srv.ServeConn(fc); err != nil {
		t.Fatal(err)
	}
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	delta := after.TotalAlloc - before.TotalAlloc
	if delta > 1<<20 {
		t.Fatalf("allocated %d bytes streaming %d-byte body; ceiling is 1 MiB", delta, n)
	}
}

func TestConnStateClosedOnce(t *testing.T) {
	cases := []struct {
		name string
		req  string
		srv  func(hit *int) *rawhttp.Server
	}{
		{
			name: "drop",
			req:  "GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n",
			srv: func(*int) *rawhttp.Server {
				return &rawhttp.Server{
					ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
					Handler: func(ctx *rawhttp.Ctx) { ctx.SetBodyString("ok") },
				}
			},
		},
		{
			name: "timeout",
			req:  "",
			srv: func(*int) *rawhttp.Server {
				return &rawhttp.Server{
					ReadHeaderTimeout: 50 * time.Millisecond,
					ReadTimeout:       50 * time.Millisecond,
					WriteTimeout:      -1,
					IdleTimeout:       -1,
					Handler:           func(ctx *rawhttp.Ctx) { ctx.SetBodyString("ok") },
				}
			},
		},
		{
			name: "413",
			req:  "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 100\r\nConnection: close\r\n\r\n",
			srv: func(*int) *rawhttp.Server {
				return &rawhttp.Server{
					ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
					MaxRequestBodySize: 8,
					Handler:            func(*rawhttp.Ctx) {},
				}
			},
		},
		{
			name: "panic",
			req:  "GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n",
			srv: func(*int) *rawhttp.Server {
				return &rawhttp.Server{
					ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
					Handler: func(*rawhttp.Ctx) { panic("boom") },
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			closed := 0
			hit := 0
			srv := tc.srv(&hit)
			prev := srv.ConnState
			srv.ConnState = func(_ net.Conn, st rawhttp.ConnState) {
				if prev != nil {
					prev(nil, st)
				}
				if st == rawhttp.StateClosed {
					mu.Lock()
					closed++
					mu.Unlock()
				}
			}
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan struct{})
			go func() {
				_ = srv.Serve(ln)
				close(done)
			}()
			c, err := net.Dial("tcp", ln.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			if tc.req != "" {
				_, _ = io.WriteString(c, tc.req)
			}
			_ = c.SetDeadline(time.Now().Add(2 * time.Second))
			_, _ = io.ReadAll(c)
			_ = c.Close()
			time.Sleep(100 * time.Millisecond)
			_ = srv.Close()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("server did not stop")
			}
			mu.Lock()
			n := closed
			mu.Unlock()
			if n != 1 {
				t.Fatalf("StateClosed %d times, want 1", n)
			}
		})
	}
}
