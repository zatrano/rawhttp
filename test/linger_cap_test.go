package test_test

import (
	"context"
	"io"
	"net"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zatrano/rawhttp"
)

func TestLingerDefaultCapBoundsFiveThousand(t *testing.T) {
	const (
		total = 5000
		capN  = 1024
	)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var lifeMu sync.Mutex
	opened := make(map[net.Conn]time.Time, total)
	var maxLife atomic.Int64
	var longLived atomic.Int64
	var peak atomic.Int64
	var peakG atomic.Int64

	srv := &rawhttp.Server{
		ReadTimeout:        -1,
		WriteTimeout:       -1,
		IdleTimeout:        -1,
		MaxRequestBodySize: 32,
		MaxLingering:       0,
		LingerTimeout:      time.Second,
		Handler:            func(*rawhttp.Ctx) {},
		ConnState: func(c net.Conn, st rawhttp.ConnState) {
			switch st {
			case rawhttp.StateNew:
				lifeMu.Lock()
				opened[c] = time.Now()
				lifeMu.Unlock()
			case rawhttp.StateClosed:
				lifeMu.Lock()
				t0, ok := opened[c]
				delete(opened, c)
				lifeMu.Unlock()
				if !ok {
					return
				}
				d := time.Since(t0)
				if d > 250*time.Millisecond {
					longLived.Add(1)
				}
				noteMax(&maxLife, int64(d))
			}
		},
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})

	stop := make(chan struct{})
	var sample sync.WaitGroup
	sample.Add(1)
	go func() {
		defer sample.Done()
		tick := time.NewTicker(time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				noteMax(&peak, srv.Lingering.Load())
				noteMax(&peakG, int64(runtime.NumGoroutine()))
			}
		}
	}()

	baseG := runtime.NumGoroutine()
	release := make(chan struct{})
	var once sync.Once
	letGo := func() { once.Do(func() { close(release) }) }
	t.Cleanup(letGo)

	addr := ln.Addr().String()
	var wg sync.WaitGroup
	var armed atomic.Int64
	var dialFail atomic.Int64
	sem := make(chan struct{}, 128)
	wg.Add(total)
	for i := 0; i < total; i++ {
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			conn, err := armEarly413(addr)
			<-sem
			if err != nil {
				dialFail.Add(1)
				return
			}
			armed.Add(1)
			<-release
			_ = conn.Close()
		}()
	}
	if !waitUntil(15*time.Second, func() bool { return armed.Load()+dialFail.Load() == total }) {
		t.Fatalf("timed out arming connections: armed %d failed %d peak lingering %d", armed.Load(), dialFail.Load(), peak.Load())
	}
	if fail := dialFail.Load(); fail != 0 {
		t.Fatalf("dialed %d failed %d peak lingering %d", armed.Load(), fail, peak.Load())
	}
	if p := peak.Load(); p > int64(capN) || p < int64(capN)-8 {
		t.Fatalf("peak lingering %d, cap %d", p, capN)
	}
	letGo()
	wg.Wait()
	close(stop)
	sample.Wait()

	if d := time.Duration(maxLife.Load()); d > 1200*time.Millisecond {
		t.Fatalf("slowest connection %s, want ≤ 1.2s", d)
	}
	time.Sleep(200 * time.Millisecond)
	if after := runtime.NumGoroutine(); after > baseG+32 {
		t.Fatalf("goroutines %d -> %d (peak during test %d)", baseG, after, peakG.Load())
	}
	t.Logf("peak lingering %d, long-lived %d, slowest %s, peak goroutines %d", peak.Load(), longLived.Load(), time.Duration(maxLife.Load()), peakG.Load())
}

func TestLingerSmallCapOverlaps(t *testing.T) {
	const capN = 4
	const n = 40
	srv, addr := serveEarly413(t, &rawhttp.Server{MaxLingering: capN, LingerTimeout: 5 * time.Second})
	var peak atomic.Int64
	stop := make(chan struct{})
	go func() {
		tick := time.NewTicker(time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				noteMax(&peak, srv.Lingering.Load())
			}
		}
	}()
	done, wait := parkEarly413s(t, addr, n)
	if !waitUntil(3*time.Second, func() bool { return done.Load() == int64(n) && peak.Load() >= int64(capN) }) {
		t.Fatalf("peak lingering %d, open %d, want %d", peak.Load(), srv.OpenConnections.Load(), capN)
	}
	if p := peak.Load(); p > capN {
		t.Fatalf("peak lingering %d exceeded cap %d", p, capN)
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	err := srv.Shutdown(ctx)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Fatalf("Shutdown during linger took %s", d)
	}
	if srv.Lingering.Load() != 0 {
		t.Fatalf("Lingering = %d after Shutdown", srv.Lingering.Load())
	}
	wait()
	close(stop)
}

func TestLingerUnlimited(t *testing.T) {
	const n = 40
	srv, addr := serveEarly413(t, &rawhttp.Server{MaxLingering: -1, LingerTimeout: 5 * time.Second})
	done, wait := parkEarly413s(t, addr, n)
	if !waitUntil(3*time.Second, func() bool { return done.Load() == int64(n) && srv.Lingering.Load() == int64(n) }) {
		t.Fatalf("Lingering = %d, open %d, want %d (unlimited)", srv.Lingering.Load(), srv.OpenConnections.Load(), n)
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	if err := srv.Shutdown(ctx); err != nil {
		cancel()
		t.Fatal(err)
	}
	cancel()
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Fatalf("Shutdown during unlimited linger took %s", d)
	}
	wait()
}

func TestLingerShutdownDuringWait(t *testing.T) {
	srv, addr := serveEarly413(t, &rawhttp.Server{LingerTimeout: 30 * time.Second})
	done, wait := parkEarly413s(t, addr, 1)
	if !waitUntil(3*time.Second, func() bool { return done.Load() == 1 && srv.Lingering.Load() == 1 }) {
		t.Fatalf("Lingering = %d, open %d, want 1", srv.Lingering.Load(), srv.OpenConnections.Load())
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	err := srv.Shutdown(ctx)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Fatalf("Shutdown took %s", d)
	}
	if srv.Lingering.Load() != 0 {
		t.Fatalf("Lingering = %d after Shutdown", srv.Lingering.Load())
	}
	wait()
}

func serveEarly413(t *testing.T, srv *rawhttp.Server) (*rawhttp.Server, string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if srv.ReadTimeout == 0 {
		srv.ReadTimeout = -1
	}
	if srv.WriteTimeout == 0 {
		srv.WriteTimeout = -1
	}
	if srv.IdleTimeout == 0 {
		srv.IdleTimeout = -1
	}
	if srv.MaxRequestBodySize == 0 {
		srv.MaxRequestBodySize = 32
	}
	if srv.Handler == nil {
		srv.Handler = func(*rawhttp.Ctx) {}
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})
	return srv, ln.Addr().String()
}

// parkEarly413s keeps each connection open until release is closed, so the
// server discard is not ended by the client FIN.
func parkEarly413s(t *testing.T, addr string, n int) (*atomic.Int64, func()) {
	t.Helper()
	release := make(chan struct{})
	var once sync.Once
	letGo := func() { once.Do(func() { close(release) }) }
	t.Cleanup(letGo)
	var wg sync.WaitGroup
	var done atomic.Int64
	errCh := make(chan error, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			conn, err := armEarly413(addr)
			if err != nil {
				done.Add(1)
				errCh <- err
				return
			}
			done.Add(1)
			<-release
			_ = conn.Close()
		}()
	}
	return &done, func() {
		letGo()
		wg.Wait()
		close(errCh)
		for err := range errCh {
			t.Error(err)
		}
	}
}

func armEarly413(addr string) (net.Conn, error) {
	conn, err := dialRetry(addr)
	if err != nil {
		return nil, err
	}
	if _, err = io.WriteString(conn, "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 10000000\r\nConnection: close\r\n\r\n"); err != nil {
		_ = conn.Close()
		return nil, err
	}
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 1024)
	_, _ = conn.Read(buf)
	_ = conn.SetReadDeadline(time.Time{})
	return conn, nil
}

func dialRetry(addr string) (net.Conn, error) {
	var conn net.Conn
	var err error
	for i := 0; i < 100; i++ {
		conn, err = net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			return conn, nil
		}
		time.Sleep(2 * time.Millisecond)
	}
	return nil, err
}

func waitUntil(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return cond()
}

func noteMax(dst *atomic.Int64, n int64) {
	for {
		cur := dst.Load()
		if n <= cur || dst.CompareAndSwap(cur, n) {
			return
		}
	}
}
