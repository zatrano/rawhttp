package test_test

import (
	"bufio"
	"context"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zatrano/rawhttp"
)

func TestFormValueFuncNetHTTP(t *testing.T) {
	body := "k=frombody"
	req := "POST /?k=fromquery HTTP/1.1\r\nHost: localhost\r\n" +
		"Content-Type: application/x-www-form-urlencoded\r\n" +
		"Content-Length: " + itoa(len(body)) + "\r\nConnection: close\r\n\r\n" + body

	var defaultVal, netHTTPVal string
	srvDefault := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) {
		defaultVal = string(ctx.FormValue("k"))
		ctx.SetBody([]byte("ok"))
	}}
	fc := newFakeConn([]byte(req))
	if err := srvDefault.ServeConn(fc); err != nil {
		t.Fatal(err)
	}
	if defaultVal != "fromquery" {
		t.Fatalf("default FormValue want fromquery, got %q", defaultVal)
	}

	srvNet := &rawhttp.Server{
		FormValueFunc: rawhttp.NetHTTPFormValueFunc,
		Handler: func(ctx *rawhttp.Ctx) {
			netHTTPVal = string(ctx.FormValue("k"))
			ctx.SetBody([]byte("ok"))
		},
	}
	fc2 := newFakeConn([]byte(req))
	if err := srvNet.ServeConn(fc2); err != nil {
		t.Fatal(err)
	}
	if netHTTPVal != "frombody" {
		t.Fatalf("NetHTTP FormValue want frombody, got %q", netHTTPVal)
	}
}

func TestFormValueFuncCustom(t *testing.T) {
	req := "GET /?a=1 HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
	srv := &rawhttp.Server{
		FormValueFunc: func(ctx *rawhttp.Ctx, key string) []byte {
			return []byte("custom:" + key)
		},
		Handler: func(ctx *rawhttp.Ctx) {
			if got := string(ctx.FormValue("x")); got != "custom:x" {
				t.Errorf("got %q", got)
			}
			ctx.SetBody([]byte("ok"))
		},
	}
	fc := newFakeConn([]byte(req))
	if err := srv.ServeConn(fc); err != nil {
		t.Fatal(err)
	}
}

func TestKeepHijackedConns(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	var hijacked net.Conn
	var wg sync.WaitGroup
	wg.Add(1)
	srv := &rawhttp.Server{
		KeepHijackedConns: true,
		Handler: func(ctx *rawhttp.Ctx) {
			conn, _, err := ctx.Hijack()
			if err != nil {
				t.Errorf("hijack: %v", err)
				wg.Done()
				return
			}
			hijacked = conn
			wg.Done()
		},
	}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	_, _ = c.Write([]byte("GET / HTTP/1.1\r\nHost: localhost\r\n\r\n"))
	wg.Wait()
	if hijacked == nil {
		t.Fatal("hijacked conn is nil")
	}
	time.Sleep(50 * time.Millisecond)
	if _, err := hijacked.Write([]byte("still-open")); err != nil {
		t.Fatalf("KeepHijackedConns should leave conn open: %v", err)
	}
	_ = hijacked.Close()
	_ = c.Close()
}

func TestKeepHijackedConnsDefaultCloses(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	var hijacked net.Conn
	done := make(chan struct{})
	srv := &rawhttp.Server{
		Handler: func(ctx *rawhttp.Ctx) {
			conn, _, err := ctx.Hijack()
			if err != nil {
				t.Errorf("hijack: %v", err)
				close(done)
				return
			}
			hijacked = conn
			close(done)
		},
	}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	_, _ = c.Write([]byte("GET / HTTP/1.1\r\nHost: localhost\r\n\r\n"))
	<-done
	time.Sleep(80 * time.Millisecond)
	_, err = hijacked.Write([]byte("x"))
	if err == nil {
		_ = hijacked.Close()
		t.Fatal("default should close hijacked conn after handler")
	}
	_ = c.Close()
}

func TestTCPDialerDialFunc(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		br := bufio.NewReader(c)
		_, _ = br.ReadString('\n')
		for {
			line, err := br.ReadString('\n')
			if err != nil || line == "\r\n" {
				break
			}
		}
		_, _ = io.WriteString(c, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nok")
	}()

	d := &rawhttp.TCPDialer{DisableDNSResolution: true, Concurrency: 10}
	hc := &rawhttp.HostClient{
		Addr:        ln.Addr().String(),
		Dial:        d.DialFunc(false),
		DialTimeout: time.Second,
	}
	defer hc.CloseIdleConnections()
	req := rawhttp.AcquireRequest()
	defer rawhttp.ReleaseRequest(req)
	req.RequestURI = "/"
	resp := rawhttp.AcquireResponse()
	defer rawhttp.ReleaseResponse(resp)
	if err := hc.Do(req, resp); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || string(resp.Body()) != "ok" {
		t.Fatalf("status=%d body=%q", resp.StatusCode, resp.Body())
	}
}

func TestTCPDialerDNSCache(t *testing.T) {
	var lookups atomic.Int32
	resolver := &countingResolver{n: &lookups, ip: net.IPv4(127, 0, 0, 1)}
	d := &rawhttp.TCPDialer{
		Resolver:         resolver,
		DNSCacheDuration: time.Hour,
		Concurrency:      4,
	}
	_, _ = d.DialTimeout("example.test:9", 50*time.Millisecond)
	_, _ = d.DialTimeout("example.test:9", 50*time.Millisecond)
	if got := lookups.Load(); got != 1 {
		t.Fatalf("lookups=%d want 1 (cached)", got)
	}
}

type countingResolver struct {
	n  *atomic.Int32
	ip net.IP
}

func (r *countingResolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	_ = ctx
	_ = host
	r.n.Add(1)
	return []net.IPAddr{{IP: r.ip}}, nil
}

func TestHTTPProxyDial(t *testing.T) {
	targetLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer targetLn.Close()
	go func() {
		c, err := targetLn.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		br := bufio.NewReader(c)
		for {
			line, err := br.ReadString('\n')
			if err != nil || line == "\r\n" {
				break
			}
		}
		_, _ = io.WriteString(c, "HTTP/1.1 200 OK\r\nContent-Length: 4\r\nConnection: close\r\n\r\nping")
	}()

	proxyLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer proxyLn.Close()
	go func() {
		c, err := proxyLn.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		br := bufio.NewReader(c)
		line, err := br.ReadString('\n')
		if err != nil {
			return
		}
		if !stringsHasPrefix(line, "CONNECT ") {
			t.Errorf("want CONNECT, got %q", line)
			return
		}
		for {
			h, err := br.ReadString('\n')
			if err != nil || h == "\r\n" {
				break
			}
		}
		_, _ = io.WriteString(c, "HTTP/1.1 200 Connection Established\r\n\r\n")
		up, err := net.Dial("tcp", targetLn.Addr().String())
		if err != nil {
			return
		}
		defer up.Close()
		errCh := make(chan struct{}, 2)
		go func() { _, _ = io.Copy(up, c); errCh <- struct{}{} }()
		go func() { _, _ = io.Copy(c, up); errCh <- struct{}{} }()
		<-errCh
	}()

	dial := rawhttp.HTTPProxyDial(proxyLn.Addr().String())
	hc := &rawhttp.HostClient{
		Addr:        targetLn.Addr().String(),
		Dial:        dial,
		DialTimeout: time.Second,
	}
	defer hc.CloseIdleConnections()
	req := rawhttp.AcquireRequest()
	defer rawhttp.ReleaseRequest(req)
	req.RequestURI = "/"
	resp := rawhttp.AcquireResponse()
	defer rawhttp.ReleaseResponse(resp)
	if err := hc.Do(req, resp); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || string(resp.Body()) != "ping" {
		t.Fatalf("status=%d body=%q", resp.StatusCode, resp.Body())
	}
}

func stringsHasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
