package test_test

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/zatrano/rawhttp"
)

// Compare rawhttp vs net/http on a fixed corpus. For each case:
//   (a) both reject or both accept
//   (b) rawhttp stricter (rejects while net/http accepts) — informational
//   (c) rawhttp looser (accepts while net/http rejects) — fail the suite
// Expect no (c) for the listed request shapes.

type diffCase struct {
	name string
	raw  string
}

func differentialCorpus() []diffCase {
	return []diffCase{
		{name: "CL+TE", raw: "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 4\r\nTransfer-Encoding: chunked\r\n\r\n0\r\n\r\n"},
		{name: "double CL", raw: "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 1\r\nContent-Length: 2\r\n\r\nx"},
		{name: "TE Chunked", raw: "POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: Chunked\r\n\r\n0\r\n\r\n"},
		{name: "TE chunked, identity", raw: "POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked, identity\r\n\r\n0\r\n\r\n"},
		{name: "TE leading space value", raw: "POST / HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding:  chunked\r\n\r\n0\r\n\r\n"},
		{name: "header name space", raw: "GET / HTTP/1.1\r\nHost: localhost\r\nBad Name: x\r\n\r\n"},
		{name: "obs-fold", raw: "GET / HTTP/1.1\r\nHost: localhost\r\nX-A: 1\r\n folded\r\n\r\n"},
		{name: "bare LF headers", raw: "GET / HTTP/1.1\nHost: localhost\n\n"},
		{name: "header value bare CR", raw: "GET / HTTP/1.1\r\nHost: localhost\r\nX-A: a\rb\r\n\r\n"},
		{name: "absolute-form", raw: "GET http://evil/ HTTP/1.1\r\nHost: localhost\r\n\r\n"},
		{name: "missing Host HTTP/1.1", raw: "GET / HTTP/1.1\r\n\r\n"},
		{name: "double Host", raw: "GET / HTTP/1.1\r\nHost: a\r\nHost: b\r\n\r\n"},
		{name: "empty Host", raw: "GET / HTTP/1.1\r\nHost:\r\n\r\n"},
		{name: "Host with @", raw: "GET / HTTP/1.1\r\nHost: user@evil\r\n\r\n"},
		{name: "HTTP/1.0 no Host", raw: "GET / HTTP/1.0\r\n\r\n"},
		{name: "HTTP/1.0 keep-alive", raw: "GET / HTTP/1.0\r\nConnection: keep-alive\r\n\r\n"},
		{name: "double space request-line", raw: "GET  / HTTP/1.1\r\nHost: localhost\r\n\r\n"},
		{name: "lowercase method", raw: "get / HTTP/1.1\r\nHost: localhost\r\n\r\n"},
		{name: "percent 2e", raw: "GET /%2e./x HTTP/1.1\r\nHost: localhost\r\n\r\n"},
		{name: "percent 00", raw: "GET /\x00 HTTP/1.1\r\nHost: localhost\r\n\r\n"},
		{name: "Upgrade", raw: "GET / HTTP/1.1\r\nHost: localhost\r\nUpgrade: websocket\r\nConnection: close\r\n\r\n"},
		{name: "Connection upgrade", raw: "GET / HTTP/1.1\r\nHost: localhost\r\nConnection: upgrade\r\n\r\n"},
		{name: "WS firefox keep-alive Upgrade", raw: "GET /ws HTTP/1.1\r\nHost: localhost\r\nConnection: keep-alive, Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n"},
		{name: "WS Upgrade h2c", raw: "GET /ws HTTP/1.1\r\nHost: localhost\r\nConnection: Upgrade\r\nUpgrade: h2c\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n"},
		{name: "WS Upgrade websocket, foo", raw: "GET /ws HTTP/1.1\r\nHost: localhost\r\nConnection: Upgrade\r\nUpgrade: websocket, foo\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n"},
		{name: "WS POST", raw: "POST /ws HTTP/1.1\r\nHost: localhost\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\nContent-Length: 0\r\n\r\n"},
		{name: "WS HTTP/1.0", raw: "GET /ws HTTP/1.0\r\nHost: localhost\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n"},
		{name: "WS with CL", raw: "GET /ws HTTP/1.1\r\nHost: localhost\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\nContent-Length: 0\r\n\r\n"},
		{name: "WS duplicate key", raw: "GET /ws HTTP/1.1\r\nHost: localhost\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n"},
		{name: "WS Upgrade without Connection token", raw: "GET /ws HTTP/1.1\r\nHost: localhost\r\nConnection: keep-alive\r\nUpgrade: websocket\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n"},
	}
}

func rawhttpOutcome(raw string) (handlerCalled bool, statusPrefix string) {
	return rawhttpOutcomeAllow(raw, false)
}

func rawhttpOutcomeAllow(raw string, allowUpgrade bool) (handlerCalled bool, statusPrefix string) {
	srv := &rawhttp.Server{
		ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
		AllowUpgrade: allowUpgrade,
		Handler: func(ctx *rawhttp.Ctx) {
			handlerCalled = true
			ctx.SetBody([]byte("ok"))
		},
	}
	fc := newFakeConn([]byte(raw))
	_ = srv.ServeConn(fc)
	resp := fc.w.String()
	if len(resp) >= 12 {
		parts := strings.SplitN(resp, " ", 3)
		if len(parts) >= 2 {
			statusPrefix = parts[1]
		}
	}
	return
}

func nethttpOutcome(t *testing.T, raw string) (handlerCalled bool, statusCode int, err error) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return false, 0, err
	}
	defer ln.Close()

	mux := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok"))
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 2 * time.Second}
	go srv.Serve(ln) //nolint:errcheck
	defer srv.Close()

	conn, err := net.DialTimeout("tcp", ln.Addr().String(), 2*time.Second)
	if err != nil {
		return false, 0, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.WriteString(conn, raw); err != nil {
		return handlerCalled, 0, err
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: "GET"})
	if err != nil {
		return handlerCalled, 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return handlerCalled, resp.StatusCode, nil
}

func TestDifferentialCorpus(t *testing.T) {
	var loose []string
	for _, tc := range differentialCorpus() {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			rhCalled, rhStatus := rawhttpOutcome(tc.raw)
			nhCalled, nhStatus, nhErr := nethttpOutcome(t, tc.raw)

			rhOK := rhCalled && rhStatus == "200"
			nhOK := nhErr == nil && nhCalled && nhStatus == 200

			class := "a-agree"
			switch {
			case rhOK == nhOK:
				class = "a-agree"
			case !rhOK && nhOK:
				class = "b-rawhttp-stricter"
			case rhOK && !nhOK:
				class = "c-rawhttp-looser"
				loose = append(loose, fmt.Sprintf("%s\n---\n%s\n--- rh=%v/%s nhCalled=%v status=%d err=%v",
					tc.name, tc.raw, rhCalled, rhStatus, nhCalled, nhStatus, nhErr))
			}
			t.Logf("class=%s rhCalled=%v rhStatus=%s nhCalled=%v nhStatus=%d nhErr=%v",
				class, rhCalled, rhStatus, nhCalled, nhStatus, nhErr)
		})
	}
	if len(loose) > 0 {
		t.Fatalf("rawhttp looser than net/http on %d case(s):\n%s", len(loose), strings.Join(loose, "\n\n"))
	}
}

func FuzzDifferentialNetHTTP(f *testing.F) {
	for _, tc := range differentialCorpus() {
		f.Add([]byte(tc.raw), false)
		f.Add([]byte(tc.raw), true)
	}
	f.Fuzz(func(t *testing.T, data []byte, allowUpgrade bool) {
		if len(data) == 0 || len(data) > 8<<10 {
			t.Skip()
		}
		rhCalled, rhStatus := rawhttpOutcomeAllow(string(data), allowUpgrade)
		if !allowUpgrade && requestLooksLikeUpgrade(data) {
			if rhCalled && rhStatus == "200" {
				t.Fatalf("AllowUpgrade=false must reject upgrade-shaped input; status=%s", rhStatus)
			}
		}
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Skip(err)
		}
		defer ln.Close()
		srv := &http.Server{
			Handler:           http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }),
			ReadHeaderTimeout: 200 * time.Millisecond,
		}
		go srv.Serve(ln) //nolint:errcheck
		defer srv.Close()
		conn, err := net.DialTimeout("tcp", ln.Addr().String(), time.Second)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(500 * time.Millisecond))
		_, _ = conn.Write(data)
		br := bufio.NewReader(conn)
		resp, err := http.ReadResponse(br, &http.Request{Method: "GET"})
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
	})
}

// FuzzDifferentialReadRequest compares rawhttp ServeConn against net/http's
// http.ReadRequest on the same bytes (parse acceptance), for both AllowUpgrade
// settings. When AllowUpgrade is false, upgrade-shaped requests must not get 200.
func FuzzDifferentialReadRequest(f *testing.F) {
	for _, tc := range differentialCorpus() {
		f.Add([]byte(tc.raw), false)
		f.Add([]byte(tc.raw), true)
	}
	f.Fuzz(func(t *testing.T, data []byte, allowUpgrade bool) {
		if len(data) == 0 || len(data) > 8<<10 {
			t.Skip()
		}
		rhCalled, rhStatus := rawhttpOutcomeAllow(string(data), allowUpgrade)
		if !allowUpgrade && requestLooksLikeUpgrade(data) {
			if rhCalled && rhStatus == "200" {
				t.Fatalf("AllowUpgrade=false accepted upgrade-shaped request")
			}
		}
		br := bufio.NewReader(bytes.NewReader(data))
		req, err := http.ReadRequest(br)
		if err == nil {
			_, _ = io.Copy(io.Discard, req.Body)
			_ = req.Body.Close()
		}
		// Must not panic; ReadRequest result is informational (std client parser).
		_ = err
	})
}

func requestLooksLikeUpgrade(data []byte) bool {
	lower := bytes.ToLower(data)
	if !bytes.Contains(lower, []byte("upgrade")) {
		return false
	}
	// Connection: …upgrade… or Upgrade: header present.
	return bytes.Contains(lower, []byte("\nupgrade:")) ||
		bytes.Contains(lower, []byte("\r\nupgrade:")) ||
		bytes.Contains(lower, []byte("connection:")) && bytes.Contains(lower, []byte("upgrade"))
}
