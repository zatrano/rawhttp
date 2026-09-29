// Command comparebench runs the same plaintext load against in-process
// rawhttp and fasthttp servers (sequential, same machine / settings).
//
//	go run ./scripts/comparebench -c 256 -d 10s
//	go run ./scripts/comparebench -c 256 -d 10s -rev   # fasthttp first
package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"runtime"
	"runtime/debug"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/valyala/fasthttp"
	"github.com/zatrano/rawhttp"
)

func main() {
	conc := flag.Int("c", 256, "concurrent clients")
	dur := flag.Duration("d", 10*time.Second, "timed duration per server (after 1s warmup)")
	rev := flag.Bool("rev", false, "run fasthttp before rawhttp")
	flag.Parse()

	order := "rawhttp then fasthttp"
	if *rev {
		order = "fasthttp then rawhttp"
	}
	fmt.Printf("comparebench: c=%d d=%s (%s)\n\n", *conc, *dur, order)

	var raw, fast result
	if *rev {
		fast = runFast(*conc, *dur)
		coolDown()
		raw = runRaw(*conc, *dur)
	} else {
		raw = runRaw(*conc, *dur)
		coolDown()
		fast = runFast(*conc, *dur)
	}

	printResult("rawhttp", raw)
	printResult("fasthttp", fast)
	if raw.rps > 0 && fast.rps > 0 {
		fmt.Printf("\nratio rawhttp/fasthttp = %.2fx throughput\n", raw.rps/fast.rps)
		if raw.p50 > 0 && fast.p50 > 0 {
			fmt.Printf("latency_p50 fasthttp/rawhttp = %.2fx (higher means rawhttp faster)\n",
				float64(fast.p50)/float64(raw.p50))
		}
		if raw.p99 > 0 && fast.p99 > 0 {
			fmt.Printf("latency_p99 fasthttp/rawhttp = %.2fx\n",
				float64(fast.p99)/float64(raw.p99))
		}
		if raw.rps < fast.rps {
			fmt.Printf("\nFAIL: fasthttp throughput > rawhttp (%.0f > %.0f)\n", fast.rps, raw.rps)
			os.Exit(2)
		}
		if raw.rps < fast.rps*1.20 {
			fmt.Printf("\nWARN: throughput margin <1.20x\n")
		}
	}
}

type result struct {
	ok, fail int64
	rps      float64
	p50, p99 time.Duration
	samples  int
}

func printResult(name string, r result) {
	fmt.Printf("=== %s ===\n", name)
	fmt.Printf("  ok=%d fail=%d\n", r.ok, r.fail)
	fmt.Printf("  throughput=%.0f req/s\n", r.rps)
	fmt.Printf("  latency_p50=%s latency_p99=%s (n=%d)\n\n",
		r.p50.Round(time.Microsecond), r.p99.Round(time.Microsecond), r.samples)
}

func coolDown() {
	runtime.GC()
	debug.FreeOSMemory()
	time.Sleep(750 * time.Millisecond)
	runtime.GC()
}

func runRaw(conc int, dur time.Duration) result {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	must(err)
	s := &rawhttp.Server{
		Handler: func(ctx *rawhttp.Ctx) {
			ctx.SetBody([]byte("Hello, World!"))
		},
		ReadTimeout:          -1,
		WriteTimeout:         -1,
		IdleTimeout:          -1,
		MaxHeaderBytes:       4096,
		NoDefaultDate:        true,
		DisablePanicRecovery: true,
		DisableRequestStats:  true,
	}
	go func() { _ = s.Serve(ln) }()
	time.Sleep(50 * time.Millisecond)
	res := loadRaw(ln.Addr().String(), conc, dur)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	_ = s.Shutdown(ctx)
	cancel()
	_ = ln.Close()
	return res
}

func runFast(conc int, dur time.Duration) result {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	must(err)
	s := &fasthttp.Server{
		Handler: func(ctx *fasthttp.RequestCtx) {
			ctx.SetBody([]byte("Hello, World!"))
		},
		ReadBufferSize:        4096,
		NoDefaultDate:         true,
		NoDefaultServerHeader: true,
	}
	go func() { _ = s.Serve(ln) }()
	time.Sleep(50 * time.Millisecond)
	res := loadFast(ln.Addr().String(), conc, dur)
	_ = s.Shutdown()
	_ = ln.Close()
	return res
}

func loadRaw(addr string, conc int, dur time.Duration) result {
	var ok, fail atomic.Int64
	latsCh := make(chan []time.Duration, conc)
	warmup := time.Second
	startAt := time.Now()
	measureAt := startAt.Add(warmup)
	stopAt := measureAt.Add(dur)
	var wg sync.WaitGroup
	for i := 0; i < conc; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			hc := &rawhttp.HostClient{
				Addr:                     addr,
				MaxConns:                 1,
				MaxIdleConnDuration:      time.Minute,
				NoDefaultUserAgentHeader: true,
				ReadBufferSize:           4096,
				MaxResponseHeaderBytes:   4096,
			}
			req := rawhttp.AcquireRequest()
			defer rawhttp.ReleaseRequest(req)
			req.Method = "GET"
			req.RequestURI = "/"
			resp := rawhttp.AcquireResponse()
			defer rawhttp.ReleaseResponse(resp)
			local := make([]time.Duration, 0, 256)
			n := 0
			for {
				now := time.Now()
				if now.After(stopAt) {
					break
				}
				measuring := !now.Before(measureAt)
				t0 := time.Now()
				err := hc.Do(req, resp)
				elapsed := time.Since(t0)
				if err != nil || resp.StatusCode != 200 {
					if measuring {
						fail.Add(1)
					}
					resp.Reset()
					continue
				}
				if measuring {
					ok.Add(1)
					n++
					if n&15 == 0 && len(local) < 256 {
						local = append(local, elapsed)
					}
				}
				resp.Reset()
			}
			hc.CloseIdleConnections()
			latsCh <- local
		}()
	}
	wg.Wait()
	close(latsCh)
	var lats []time.Duration
	for part := range latsCh {
		lats = append(lats, part...)
	}
	return summarize(ok.Load(), fail.Load(), dur, lats)
}

func loadFast(addr string, conc int, dur time.Duration) result {
	var ok, fail atomic.Int64
	latsCh := make(chan []time.Duration, conc)
	warmup := time.Second
	startAt := time.Now()
	measureAt := startAt.Add(warmup)
	stopAt := measureAt.Add(dur)
	var wg sync.WaitGroup
	for i := 0; i < conc; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := &fasthttp.HostClient{Addr: addr, MaxConns: 1, ReadBufferSize: 4096}
			req := fasthttp.AcquireRequest()
			defer fasthttp.ReleaseRequest(req)
			resp := fasthttp.AcquireResponse()
			defer fasthttp.ReleaseResponse(resp)
			req.SetRequestURI("http://" + addr + "/")
			req.Header.SetMethod("GET")
			local := make([]time.Duration, 0, 256)
			n := 0
			for {
				now := time.Now()
				if now.After(stopAt) {
					break
				}
				measuring := !now.Before(measureAt)
				t0 := time.Now()
				err := c.Do(req, resp)
				elapsed := time.Since(t0)
				if err != nil || resp.StatusCode() != 200 {
					if measuring {
						fail.Add(1)
					}
					resp.Reset()
					continue
				}
				if measuring {
					ok.Add(1)
					n++
					if n&15 == 0 && len(local) < 256 {
						local = append(local, elapsed)
					}
				}
				resp.Reset()
			}
			c.CloseIdleConnections()
			latsCh <- local
		}()
	}
	wg.Wait()
	close(latsCh)
	var lats []time.Duration
	for part := range latsCh {
		lats = append(lats, part...)
	}
	return summarize(ok.Load(), fail.Load(), dur, lats)
}

func summarize(ok, fail int64, dur time.Duration, lats []time.Duration) result {
	r := result{ok: ok, fail: fail, rps: float64(ok) / dur.Seconds(), samples: len(lats)}
	sort.Slice(lats, func(i, j int) bool { return lats[i] < lats[j] })
	if n := len(lats); n > 0 {
		r.p50 = lats[n/2]
		r.p99 = lats[(n*99)/100]
	}
	return r
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
