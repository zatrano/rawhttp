package test_test

import (
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zatrano/rawhttp"
)

func TestRateLimitMiddleware(t *testing.T) {
	var n atomic.Int32
	h := rawhttp.RateLimitMiddleware(func(ctx *rawhttp.Ctx) {
		n.Add(1)
		ctx.SetBody([]byte("ok"))
	}, rawhttp.RateLimitOptions{Rate: 1, Burst: 1, Key: func(ctx *rawhttp.Ctx) string { return "fixed" }})
	srv := &rawhttp.Server{Handler: h, ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1}

	fc := newFakeConn([]byte("GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "ok") || n.Load() != 1 {
		t.Fatalf("first should pass: %q n=%d", fc.w.String(), n.Load())
	}

	fc = newFakeConn([]byte("GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "429") {
		t.Fatalf("second should 429: %q", fc.w.String())
	}
	if n.Load() != 1 {
		t.Fatalf("handler must not run on reject: n=%d", n.Load())
	}

	time.Sleep(1100 * time.Millisecond)
	fc = newFakeConn([]byte("GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "ok") {
		t.Fatalf("after refill: %q", fc.w.String())
	}
}

func TestWriteJSONReadJSON(t *testing.T) {
	type pair struct {
		A int    `json:"a"`
		B string `json:"b"`
	}
	var ran bool
	srv := &rawhttp.Server{
		Handler: func(ctx *rawhttp.Ctx) {
			ran = true
			var in pair
			if err := ctx.ReadJSON(&in); err != nil {
				ctx.Error(err.Error(), 400)
				return
			}
			if err := ctx.WriteJSON(pair{A: in.A + 1, B: in.B + "!"}); err != nil {
				ctx.Error(err.Error(), 500)
				return
			}
		},
		ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
	}
	body := []byte(`{"a":1,"b":"hi"}`)
	req := append([]byte("POST / HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/json\r\nContent-Length: "),
		[]byte(strconv.Itoa(len(body)))...)
	req = append(req, []byte("\r\nConnection: close\r\n\r\n")...)
	req = append(req, body...)
	fc := newFakeConn(req)
	err := srv.ServeConn(fc)
	if err != nil {
		t.Fatalf("ServeConn: %v resp=%q", err, fc.w.String())
	}
	if !ran {
		t.Fatal("handler did not run")
	}
	resp := fc.w.String()
	if !strings.Contains(resp, "application/json") || !strings.Contains(resp, `"a":2`) || !strings.Contains(resp, `"b":"hi!"`) {
		t.Fatalf("json resp: %q", resp)
	}
}

func TestClientSetJSON(t *testing.T) {
	var gotCT string
	var gotBody string
	addr, stop := startTestServer(t, func(ctx *rawhttp.Ctx) {
		gotCT = string(ctx.RequestContentType())
		gotBody = string(ctx.Body())
		ctx.SetBody([]byte(`{"ok":true}`))
	})
	defer stop()
	c := &rawhttp.Client{}
	req := rawhttp.AcquireRequest()
	defer rawhttp.ReleaseRequest(req)
	req.Method = "POST"
	req.RequestURI = "http://" + addr + "/"
	if err := req.SetJSON(map[string]int{"n": 7}); err != nil {
		t.Fatal(err)
	}
	resp := rawhttp.AcquireResponse()
	defer rawhttp.ReleaseResponse(resp)
	if err := c.Do(req, resp); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(gotCT, "application/json") {
		t.Fatalf("ct=%q", gotCT)
	}
	if !strings.Contains(gotBody, `"n":7`) {
		t.Fatalf("body=%q", gotBody)
	}
	var out struct {
		OK bool `json:"ok"`
	}
	if err := resp.JSON(&out); err != nil || !out.OK {
		t.Fatalf("resp json: %v %#v", err, out)
	}
	if resp.BodyString() == "" {
		t.Fatal("BodyString empty")
	}
}
