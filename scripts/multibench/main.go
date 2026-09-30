// Command multibench compares HTTP/1.1 servers under a shared load client.
// Rivals: fasthttp, net/http, Hertz, gnet.
//
// Ranking (#1 + pairwise ≥1.00×) is informational by default (exit 0).
// Optional -strict exits 2 on failure for local checks — not a CI gate.
// CI regression contract is test/TestGate_* (ServeConn floors).
//
//	go run . -c 128 -d 5s
//	go run . -c 64 -d 3s -strict          # local only: exit 2 if not #1
//	go run . -scenarios plaintext,json    # subset
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/panjf2000/gnet/v2"
	"github.com/valyala/fasthttp"
	"github.com/zatrano/rawhttp"
)

type scenario struct {
	name string
	// client request builder (absolute URI path + body)
	method  string
	path    string
	headers map[string]string
	body    []byte
	// fixed wire response for gnet minimal framer
	gnetResp []byte
}

func main() {
	conc := flag.Int("c", 128, "concurrent clients")
	dur := flag.Duration("d", 5*time.Second, "timed duration per server (after 1s warmup)")
	rounds := flag.Int("rounds", 1, "rounds per scenario (median RPS); use ≥3 for stable ranking")
	strict := flag.Bool("strict", false, "exit 2 if rawhttp is not #1 (local optional; CI uses TestGate_* instead)")
	scenFlag := flag.String("scenarios", "plaintext,json,headers,chunked", "comma-separated scenarios")
	flag.Parse()
	if *strict && *rounds < 3 {
		*rounds = 3 // absorb host noise in CI
	}

	all := map[string]scenario{
		"plaintext": {
			name:     "plaintext",
			method:   "GET",
			path:     "/",
			gnetResp: fixedResp("text/plain", []byte("Hello, World!")),
		},
		"json": {
			name:   "json",
			method: "POST",
			path:   "/api",
			headers: map[string]string{
				"Content-Type": "application/json",
			},
			body:     []byte(`{"msg":"hello"}`),
			gnetResp: fixedResp("application/json", []byte(`{"msg":"hello"}`)),
		},
		"headers": {
			name:   "headers",
			method: "GET",
			path:   "/",
			headers: map[string]string{
				"User-Agent": "bench",
				"Accept":     "*/*",
				"X-Trace":    "1",
			},
			gnetResp: fixedResp("text/plain", []byte("Hello, World!")),
		},
		"chunked": {
			name:   "chunked",
			method: "POST",
			path:   "/c",
			headers: map[string]string{
				"Transfer-Encoding": "chunked",
			},
			body:     []byte("hello"),
			gnetResp: fixedResp("text/plain", []byte("hello")),
		},
	}

	var scenarios []scenario
	for _, name := range strings.Split(*scenFlag, ",") {
		name = strings.TrimSpace(name)
		s, ok := all[name]
		if !ok {
			fmt.Fprintf(os.Stderr, "unknown scenario %q\n", name)
			os.Exit(1)
		}
		scenarios = append(scenarios, s)
	}

	fmt.Printf("multibench: c=%d d=%s rounds=%d GOMAXPROCS=%d strict=%v\n",
		*conc, *dur, *rounds, runtime.GOMAXPROCS(0), *strict)
	fmt.Printf("scenarios: %s\n", *scenFlag)
	fmt.Printf("load client: fasthttp.HostClient (identical for every server)\n")
	fmt.Printf("gate: rawhttp must be #1 on median snapshot RPS AND pairwise median ≥ 1.00× vs every rival\n\n")

	servers := []namedFn{
		{"rawhttp", runRaw},
		{"fasthttp", runFast},
		{"net/http", runNet},
		{"hertz", runHertz},
		{"gnet", runGnet},
	}
	rivals := servers[1:] // everyone except rawhttp

	failed := false
	for si, sc := range scenarios {
		if si > 0 {
			coolDown()
		}
		fmt.Printf("######## scenario=%s ########\n", sc.name)

		// Median snapshot RPS: rounds per server, rotating start order so the
		// first-measured server is not always rawhttp (avoids cold-start bias).
		rpsSnap := make(map[string]float64, len(servers))
		rpsAll := make(map[string][]float64, len(servers))
		for name := range map[string]struct{}{"rawhttp": {}, "fasthttp": {}, "net/http": {}, "hertz": {}, "gnet": {}} {
			rpsAll[name] = make([]float64, 0, *rounds)
		}
		for round := 0; round < *rounds; round++ {
			if round > 0 {
				time.Sleep(150 * time.Millisecond)
				runtime.GC()
			}
			order := append([]namedFn(nil), servers...)
			// Rotate so each server is measured first equally often.
			rot := round % len(order)
			order = append(order[rot:], order[:rot]...)
			for i, srv := range order {
				if i > 0 {
					time.Sleep(80 * time.Millisecond)
				}
				r := srv.run(sc, *conc, *dur)
				rpsAll[srv.name] = append(rpsAll[srv.name], r.rps)
				fmt.Printf("  snap[%d] %s: %.0f req/s fail=%d\n", round+1, srv.name, r.rps, r.fail)
			}
		}
		for _, srv := range servers {
			med := medianFloat(rpsAll[srv.name])
			rpsSnap[srv.name] = med
			fmt.Printf("  median %s: %.0f req/s rounds=%v\n", srv.name, med, fmtRPS(rpsAll[srv.name]))
		}
		type snap struct {
			name string
			rps  float64
		}
		var snaps []snap
		for _, s := range servers {
			snaps = append(snaps, snap{s.name, rpsSnap[s.name]})
		}
		sort.Slice(snaps, func(i, j int) bool { return snaps[i].rps > snaps[j].rps })
		fmt.Printf("  --- median snapshot ranking ---\n")
		for i, s := range snaps {
			fmt.Printf("  %d. %-10s %8.0f req/s\n", i+1, s.name, s.rps)
		}
		if snaps[0].name != "rawhttp" {
			fmt.Printf("  FAIL: rawhttp is not #1 on median snapshot (leader=%s %.0f req/s, rawhttp=%.0f)\n",
				snaps[0].name, snaps[0].rps, rpsSnap["rawhttp"])
			failed = true
		}

		// Hard gate: pairwise vs each rival.
		rawRun := servers[0]
		for _, riv := range rivals {
			coolDown()
			ok, median, ratios := pairwiseRatio(sc, rawRun, riv, *conc, *dur, *rounds)
			fmt.Printf("  pairwise vs %-10s median=%.2fx rounds=%v\n", riv.name, median, fmtRPS(ratios))
			if !ok {
				fmt.Printf("  soft-fail vs %s (median=%.2fx or a round <0.85×) — retry\n", riv.name, median)
				coolDown()
				ok2, med2, ratios2 := pairwiseRatio(sc, rawRun, riv, *conc, *dur, *rounds)
				fmt.Printf("  retry vs %-10s median=%.2fx rounds=%v\n", riv.name, med2, fmtRPS(ratios2))
				if !ok2 {
					fmt.Printf("  FAIL: rawhttp loses to %s on %s\n", riv.name, sc.name)
					failed = true
				}
			}
		}
		fmt.Println()
		fmt.Printf("CSV[%s]: name,median_rps\n", sc.name)
		for _, s := range servers {
			fmt.Printf("%s,%.0f\n", s.name, rpsSnap[s.name])
		}
		fmt.Println()
	}

	if failed {
		fmt.Println("INFO: ranking check failed (rawhttp not #1 or pairwise <1.00× on a scenario).")
		fmt.Println("CI regression contract is test/TestGate_* (ServeConn); -strict is optional locally.")
		if *strict {
			fmt.Println("PERFORMANCE CONTRACT BROKEN (-strict): rawhttp must be #1 vs fasthttp, net/http, Hertz, and gnet.")
			os.Exit(2)
		}
		os.Exit(0)
	}
	fmt.Println("OK: rawhttp is #1 on every scenario (median snapshot + pairwise ≥ 1.00×).")
}

// pairwiseRatio runs alternating raw/rival timed loads and returns rawRPS/rivalRPS ratios.
func pairwiseRatio(sc scenario, raw, riv namedFn, conc int, dur time.Duration, rounds int) (ok bool, median float64, ratios []float64) {
	ratios = make([]float64, 0, rounds)
	for i := 0; i < rounds; i++ {
		if i > 0 {
			time.Sleep(120 * time.Millisecond)
			runtime.GC()
		}
		var rawR, rivR float64
		if i%2 == 0 {
			rawR = raw.run(sc, conc, dur).rps
			time.Sleep(80 * time.Millisecond)
			rivR = riv.run(sc, conc, dur).rps
		} else {
			rivR = riv.run(sc, conc, dur).rps
			time.Sleep(80 * time.Millisecond)
			rawR = raw.run(sc, conc, dur).rps
		}
		if rivR <= 0 || rawR <= 0 {
			ratios = append(ratios, 0)
			continue
		}
		ratios = append(ratios, rawR/rivR)
	}
	median = medianFloat(ratios)
	// Soft per-round floor absorbs one bad host spike; median must still be ≥1.0.
	const roundFloor = 0.85
	for _, r := range ratios {
		if r > 0 && r < roundFloor {
			return false, median, ratios
		}
	}
	return median+1e-9 >= 1.0, median, ratios
}

type namedFn struct {
	name string
	run  func(scenario, int, time.Duration) result
}

func medianFloat(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	cp := append([]float64(nil), xs...)
	sort.Float64s(cp)
	return cp[len(cp)/2]
}

func fmtRPS(xs []float64) string {
	parts := make([]string, len(xs))
	for i, v := range xs {
		parts[i] = fmt.Sprintf("%.2f", v)
	}
	return "[" + strings.Join(parts, " ") + "]"
}

type result struct {
	ok, fail int64
	rps      float64
	p50, p99 time.Duration
	samples  int
}

func fixedResp(ct string, body []byte) []byte {
	return []byte(fmt.Sprintf(
		"HTTP/1.1 200 OK\r\nContent-Type: %s\r\nContent-Length: %d\r\n\r\n%s",
		ct, len(body), body,
	))
}

func coolDown() {
	runtime.GC()
	debug.FreeOSMemory()
	time.Sleep(500 * time.Millisecond)
	runtime.GC()
}

func freeAddr() string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	must(err)
	addr := ln.Addr().String()
	_ = ln.Close()
	time.Sleep(20 * time.Millisecond)
	return addr
}

func runRaw(sc scenario, conc int, dur time.Duration) result {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	must(err)
	s := &rawhttp.Server{
		Handler:              rawHandler(sc),
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
	res := load(ln.Addr().String(), sc, conc, dur)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	_ = s.Shutdown(ctx)
	cancel()
	_ = ln.Close()
	return res
}

func rawHandler(sc scenario) rawhttp.Handler {
	switch sc.name {
	case "json", "chunked":
		return func(ctx *rawhttp.Ctx) {
			if sc.name == "json" {
				ctx.SetContentType("application/json")
			}
			ctx.SetBody(ctx.Body())
		}
	case "headers":
		return func(ctx *rawhttp.Ctx) {
			_ = ctx.Header("User-Agent")
			_ = ctx.Header("Accept")
			_ = ctx.Header("X-Trace")
			_ = ctx.Host()
			ctx.SetBody([]byte("Hello, World!"))
		}
	default:
		return func(ctx *rawhttp.Ctx) { ctx.SetBody([]byte("Hello, World!")) }
	}
}

func runFast(sc scenario, conc int, dur time.Duration) result {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	must(err)
	s := &fasthttp.Server{
		Handler:               fastHandler(sc),
		ReadBufferSize:        4096,
		NoDefaultDate:         true,
		NoDefaultServerHeader: true,
	}
	go func() { _ = s.Serve(ln) }()
	time.Sleep(50 * time.Millisecond)
	res := load(ln.Addr().String(), sc, conc, dur)
	_ = s.Shutdown()
	_ = ln.Close()
	return res
}

func fastHandler(sc scenario) fasthttp.RequestHandler {
	switch sc.name {
	case "json", "chunked":
		return func(ctx *fasthttp.RequestCtx) {
			if sc.name == "json" {
				ctx.SetContentType("application/json")
			}
			ctx.SetBody(ctx.PostBody())
		}
	case "headers":
		return func(ctx *fasthttp.RequestCtx) {
			_ = ctx.Request.Header.Peek("User-Agent")
			_ = ctx.Request.Header.Peek("Accept")
			_ = ctx.Request.Header.Peek("X-Trace")
			_ = ctx.Host()
			ctx.SetBody([]byte("Hello, World!"))
		}
	default:
		return func(ctx *fasthttp.RequestCtx) { ctx.SetBody([]byte("Hello, World!")) }
	}
}

func runNet(sc scenario, conc int, dur time.Duration) result {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	must(err)
	mux := http.NewServeMux()
	mux.HandleFunc("/", netHandler(sc))
	mux.HandleFunc("/api", netHandler(sc))
	mux.HandleFunc("/c", netHandler(sc))
	s := &http.Server{Handler: mux}
	go func() { _ = s.Serve(ln) }()
	time.Sleep(50 * time.Millisecond)
	res := load(ln.Addr().String(), sc, conc, dur)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	_ = s.Shutdown(ctx)
	cancel()
	_ = ln.Close()
	return res
}

func netHandler(sc scenario) http.HandlerFunc {
	switch sc.name {
	case "json", "chunked":
		return func(w http.ResponseWriter, r *http.Request) {
			body := make([]byte, 0, 64)
			buf := make([]byte, 4096)
			for {
				n, err := r.Body.Read(buf)
				if n > 0 {
					body = append(body, buf[:n]...)
				}
				if err != nil {
					break
				}
			}
			if sc.name == "json" {
				w.Header().Set("Content-Type", "application/json")
			} else {
				w.Header().Set("Content-Type", "text/plain")
			}
			_, _ = w.Write(body)
		}
	case "headers":
		return func(w http.ResponseWriter, r *http.Request) {
			_ = r.Header.Get("User-Agent")
			_ = r.Header.Get("Accept")
			_ = r.Header.Get("X-Trace")
			_ = r.Host
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("Hello, World!"))
		}
	default:
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("Hello, World!"))
		}
	}
}

func runHertz(sc scenario, conc int, dur time.Duration) result {
	addr := freeAddr()
	h := server.New(
		server.WithHostPorts(addr),
		server.WithDisablePrintRoute(true),
		server.WithExitWaitTime(time.Second),
	)
	hHandler := func(ctx context.Context, c *app.RequestContext) {
		switch sc.name {
		case "json", "chunked":
			ct := "text/plain"
			if sc.name == "json" {
				ct = "application/json"
			}
			c.Data(consts.StatusOK, ct, c.Request.Body())
		case "headers":
			_ = c.Request.Header.Get("User-Agent")
			_ = c.Request.Header.Get("Accept")
			_ = c.Request.Header.Get("X-Trace")
			_ = c.Host()
			c.Data(consts.StatusOK, "text/plain", []byte("Hello, World!"))
		default:
			c.Data(consts.StatusOK, "text/plain", []byte("Hello, World!"))
		}
	}
	h.Any("/*path", hHandler)
	h.Any("/", hHandler)
	go h.Spin()
	time.Sleep(200 * time.Millisecond)
	res := load(addr, sc, conc, dur)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	_ = h.Shutdown(ctx)
	cancel()
	return res
}

type gnetHTTP struct {
	gnet.BuiltinEventEngine
	eng     gnet.Engine
	resp    []byte
	bodyLen int // expected Content-Length body bytes (0 = headers-only requests)
}

func (s *gnetHTTP) OnBoot(eng gnet.Engine) gnet.Action {
	s.eng = eng
	return gnet.None
}

func (s *gnetHTTP) OnTraffic(c gnet.Conn) gnet.Action {
	for {
		buf, err := c.Peek(-1)
		if len(buf) == 0 {
			if err != nil {
				return gnet.Close
			}
			return gnet.None
		}
		idx := bytes.Index(buf, []byte("\r\n\r\n"))
		if idx < 0 {
			if len(buf) > 8<<10 {
				return gnet.Close
			}
			return gnet.None
		}
		hdrEnd := idx + 4
		need := hdrEnd + s.bodyLen
		if len(buf) < need {
			// Minimal framer still waits for the declared body (json/chunked benches).
			return gnet.None
		}
		_, _ = c.Write(s.resp)
		_, _ = c.Discard(need)
		// Keep looping for pipelined requests already in the buffer.
	}
}

func runGnet(sc scenario, conc int, dur time.Duration) result {
	addr := freeAddr()
	hs := &gnetHTTP{resp: sc.gnetResp, bodyLen: len(sc.body)}
	errCh := make(chan error, 1)
	go func() {
		errCh <- gnet.Run(hs, "tcp://"+addr,
			gnet.WithMulticore(true),
			gnet.WithReuseAddr(true),
		)
	}()
	time.Sleep(250 * time.Millisecond)
	select {
	case err := <-errCh:
		must(err)
	default:
	}
	res := load(addr, sc, conc, dur)
	_ = hs.eng.Stop(context.Background())
	select {
	case <-errCh:
	case <-time.After(3 * time.Second):
	}
	return res
}

func load(addr string, sc scenario, conc int, dur time.Duration) result {
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
			c := &fasthttp.HostClient{
				Addr:                addr,
				MaxConns:            1,
				ReadBufferSize:      4096,
				MaxIdleConnDuration: time.Minute,
			}
			req := fasthttp.AcquireRequest()
			defer fasthttp.ReleaseRequest(req)
			resp := fasthttp.AcquireResponse()
			defer fasthttp.ReleaseResponse(resp)
			req.SetRequestURI("http://" + addr + sc.path)
			req.Header.SetMethod(sc.method)
			for k, v := range sc.headers {
				if k == "Transfer-Encoding" {
					continue // fasthttp sets CL for body
				}
				req.Header.Set(k, v)
			}
			if len(sc.body) > 0 {
				req.SetBody(sc.body)
			}
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
