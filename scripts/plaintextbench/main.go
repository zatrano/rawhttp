// Command plaintextbench runs a TechEmpower-style plaintext load test against
// an in-process rawhttp server (no external wrk/hey required).
//
// Usage:
//
//	go run ./scripts/plaintextbench -c 256 -d 10s
//	go run ./scripts/plaintextbench -addr 127.0.0.1:8080 -c 512 -d 30s  # external server
package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zatrano/rawhttp"
)

func main() {
	addr := flag.String("addr", "", "existing server addr (host:port); empty starts an in-process server")
	conc := flag.Int("c", 256, "concurrent clients")
	dur := flag.Duration("d", 5*time.Second, "test duration")
	keepalive := flag.Bool("k", true, "HTTP keep-alive")
	flag.Parse()

	target := *addr
	var cleanup func()
	if target == "" {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			fatal(err)
		}
		s := &rawhttp.Server{
			Handler: func(ctx *rawhttp.Ctx) {
				ctx.SetContentType("text/plain")
				ctx.SetBody([]byte("Hello, World!"))
			},
			ReadTimeout:  -1,
			WriteTimeout: -1,
			IdleTimeout:  -1,
		}
		go func() { _ = s.Serve(ln) }()
		target = ln.Addr().String()
		cleanup = func() { _ = ln.Close() }
		fmt.Printf("started in-process server on %s\n", target)
	}
	if cleanup != nil {
		defer cleanup()
	}

	fmt.Printf("plaintext bench: addr=%s c=%d d=%s keepalive=%v\n", target, *conc, *dur, *keepalive)

	var (
		ok     atomic.Int64
		fail   atomic.Int64
		bytes  atomic.Int64
		latMu  sync.Mutex
		lats   []time.Duration
		stopAt = time.Now().Add(*dur)
	)

	var wg sync.WaitGroup
	for i := 0; i < *conc; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := &rawhttp.Client{
				MaxConnsPerHost:     1,
				MaxIdleConnDuration: time.Minute,
				ReadTimeout:         5 * time.Second,
				WriteTimeout:        5 * time.Second,
				DialTimeout:         2 * time.Second,
			}
			url := "http://" + target + "/"
			req := rawhttp.AcquireRequest()
			defer rawhttp.ReleaseRequest(req)
			req.Method = "GET"
			req.RequestURI = url
			if !*keepalive {
				_ = req.SetHeader("Connection", "close")
			}
			resp := rawhttp.AcquireResponse()
			defer rawhttp.ReleaseResponse(resp)

			for time.Now().Before(stopAt) {
				start := time.Now()
				err := c.Do(req, resp)
				elapsed := time.Since(start)
				if err != nil || resp.StatusCode != 200 {
					fail.Add(1)
					resp.Reset()
					continue
				}
				ok.Add(1)
				bytes.Add(int64(len(resp.Body())))
				latMu.Lock()
				if len(lats) < 200_000 {
					lats = append(lats, elapsed)
				}
				latMu.Unlock()
				resp.Reset()
			}
			c.CloseIdleConnections()
		}()
	}
	wg.Wait()

	totalOK := ok.Load()
	totalFail := fail.Load()
	elapsed := *dur
	rps := float64(totalOK) / elapsed.Seconds()

	sort.Slice(lats, func(i, j int) bool { return lats[i] < lats[j] })
	p50, p99 := time.Duration(0), time.Duration(0)
	if n := len(lats); n > 0 {
		p50 = lats[n/2]
		p99 = lats[(n*99)/100]
	}

	fmt.Printf("requests_ok=%d requests_fail=%d\n", totalOK, totalFail)
	fmt.Printf("throughput=%.0f req/s  bytes=%d\n", rps, bytes.Load())
	fmt.Printf("latency_p50=%s latency_p99=%s samples=%d\n", p50.Round(time.Microsecond), p99.Round(time.Microsecond), len(lats))
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
