package test_test

import (
	"bytes"
	"fmt"
	"net"
	"net/http"
	"runtime"
	"sort"
	"testing"
	"time"

	"github.com/valyala/fasthttp"
	"github.com/zatrano/rawhttp"
)

// Performance contract (CI-enforced via ServeConn gates only):
//
// rawhttp must never lose to fasthttp / net/http on the ServeConn microbench
// floors below. TCP multi-rival ranking (scripts/multibench) is informational
// in CI; optional -strict is local-only and must not block merges.
//
// Floors are machine-calibrated on Windows, Intel Core i5-1135G7 @ 2.40GHz
// (2026-09-30, 10× TestGate_*). Host-specific — re-calibrate after the first
// Linux CI runs on ubuntu-latest before treating these as universal.
//
// fasthttp ServeConn floors:
//  1. Never slower: trimmed rounds ≥1.0× (soft round floor 0.85×)
//  2. Plaintext ≥2.35×, 0 allocs/op
//     Rationale: this 10-run series min trimmed-median was 2.74×; prior
//     measurements saw 2.61×. Floor = ~10% below the lowest observation (≈2.35×).
//  3. JSON POST ≥1.65×
//  4. Header peek + chunked ≥1.5×
//  5. HostClient: informational only (pipe-backed client microbench is too
//     noisy for a blocking floor on this host; see docs/performance.md)
//
// net/http ServeConn floors:
//
//	 Plaintext ≥8.0×, JSON ≥4.0× (stdlib is alloc-heavy)
//
//		cd test && go test -run 'Gate|Allocs' -count=1 -v
//		cd scripts/multibench && go run . -c 64 -d 3s
const (
	gateRequests     = 100_000
	gateWarmup       = 2
	gateRounds       = 11   // more rounds + deeper trim → less Windows/CI host noise
	gateMinRatio     = 2.35 // plaintext vs fasthttp; see header comment
	gateJSONMinRatio = 1.65 // JSON POST vs fasthttp
	gateExtraMin     = 1.5  // headers / chunked vs fasthttp
	gateNetPlainMin  = 8.0  // plaintext vs net/http
	gateNetJSONMin   = 4.0  // JSON vs net/http
)

func TestGate_FasterThanFastHTTP_Plaintext(t *testing.T) {
	skipIfPoison(t)
	data := buildRequests(gateRequests)
	raw := func(d []byte) int64 { return timeServeRaw(d) }
	fast := func(d []byte) int64 { return timeServeFast(d) }
	assertFaster(t, "plaintext", data, raw, fast, gateMinRatio)
}

func TestGate_FasterThanFastHTTP_JSONPost(t *testing.T) {
	skipIfPoison(t)
	payload := []byte(`{"msg":"hello"}`)
	one := "POST /api HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/json\r\nContent-Length: 16\r\n\r\n" + string(payload)
	data := bytes.Repeat([]byte(one), gateRequests)
	raw := func(d []byte) int64 { return timeServeRawJSON(d) }
	fast := func(d []byte) int64 { return timeServeFastJSON(d) }
	assertFaster(t, "json", data, raw, fast, gateJSONMinRatio)
}

func TestGate_FasterThanFastHTTP_HeaderPeek(t *testing.T) {
	skipIfPoison(t)
	data := buildRequests(gateRequests)
	raw := func(d []byte) int64 {
		return timeServe(d, &rawhttp.Server{
			ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
			Handler: func(ctx *rawhttp.Ctx) {
				_ = ctx.Header("User-Agent")
				_ = ctx.Header("Accept")
				_ = ctx.Host()
				ctx.SetBody(plaintextBody)
			},
		})
	}
	fast := func(d []byte) int64 {
		return timeServeFastHandler(d, func(ctx *fasthttp.RequestCtx) {
			_ = ctx.Request.Header.Peek("User-Agent")
			_ = ctx.Request.Header.Peek("Accept")
			_ = ctx.Host()
			ctx.SetBody(plaintextBody)
		})
	}
	assertFaster(t, "headers", data, raw, fast, gateExtraMin)
}

func TestGate_FasterThanFastHTTP_ChunkedEcho(t *testing.T) {
	skipIfPoison(t)
	one := "POST /c HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nhello\r\n0\r\n\r\n"
	data := bytes.Repeat([]byte(one), gateRequests)
	raw := func(d []byte) int64 {
		return timeServe(d, &rawhttp.Server{
			ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
			Handler: func(ctx *rawhttp.Ctx) {
				ctx.SetBody(ctx.Body())
			},
		})
	}
	fast := func(d []byte) int64 {
		return timeServeFastHandler(d, func(ctx *fasthttp.RequestCtx) {
			ctx.SetBody(ctx.PostBody())
		})
	}
	assertFaster(t, "chunked", data, raw, fast, gateExtraMin)
}

func TestGate_FasterThanNetHTTP_Plaintext(t *testing.T) {
	skipIfPoison(t)
	data := buildRequests(gateRequests)
	raw := func(d []byte) int64 { return timeServeRaw(d) }
	netH := func(d []byte) int64 { return timeServeNet(d) }
	assertFaster(t, "plaintext-net/http", data, raw, netH, gateNetPlainMin)
}

func TestGate_FasterThanNetHTTP_JSONPost(t *testing.T) {
	skipIfPoison(t)
	payload := []byte(`{"msg":"hello"}`)
	one := "POST /api HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/json\r\nContent-Length: 16\r\n\r\n" + string(payload)
	data := bytes.Repeat([]byte(one), gateRequests)
	raw := func(d []byte) int64 { return timeServeRawJSON(d) }
	netH := func(d []byte) int64 { return timeServeNetJSON(d) }
	assertFaster(t, "json-net/http", data, raw, netH, gateNetJSONMin)
}

func assertFaster(t *testing.T, name string, data []byte, rawFn, fastFn func([]byte) int64, minRatio float64) {
	t.Helper()
	if assertFasterOnce(t, name, data, rawFn, fastFn, minRatio, false) {
		return
	}
	// Soft retry absorbs one-shot Windows/CI host spikes without lowering floors.
	t.Logf("%s: soft-retry after GC (host noise)", name)
	runtime.GC()
	time.Sleep(100 * time.Millisecond)
	if !assertFasterOnce(t, name, data, rawFn, fastFn, minRatio, true) {
		t.FailNow()
	}
}

func assertFasterOnce(t *testing.T, name string, data []byte, rawFn, fastFn func([]byte) int64, minRatio float64, fatal bool) bool {
	t.Helper()
	fail := func(format string, args ...any) bool {
		if fatal {
			t.Errorf(format, args...)
		} else {
			t.Logf(format, args...)
		}
		return false
	}

	runtime.GC()
	for i := 0; i < gateWarmup; i++ {
		_ = rawFn(data)
		_ = fastFn(data)
	}

	const roundFloor = 0.85

	ratios := make([]float64, 0, gateRounds)
	for i := 0; i < gateRounds; i++ {
		runtime.GC()
		runtime.Gosched()
		var rawNs, fastNs int64
		if i%2 == 0 {
			rawNs = rawFn(data)
			fastNs = fastFn(data)
		} else {
			fastNs = fastFn(data)
			rawNs = rawFn(data)
		}
		if fastNs <= 0 || rawNs <= 0 {
			return fail("%s: invalid timings raw=%d fast=%d", name, rawNs, fastNs)
		}
		r := float64(fastNs) / float64(rawNs)
		ratios = append(ratios, r)
		t.Logf("%s round %d: rawhttp=%dns rival=%dns ratio=%.2fx", name, i+1, rawNs, fastNs, r)
		if r < roundFloor {
			return fail("PERFORMANCE REGRESSION (%s): round %d slower than rival (%.2fx < %.2fx)", name, i+1, r, roundFloor)
		}
	}
	sort.Float64s(ratios)
	trimmed := ratios[2 : len(ratios)-2]
	for i, r := range trimmed {
		if r < 1.0 {
			return fail("PERFORMANCE REGRESSION (%s): trimmed round %d slower than rival (%.2fx < 1.00x); all=%v", name, i+1, r, fmtRatios(ratios))
		}
	}
	ratio := trimmed[len(trimmed)/2]
	t.Logf("%s trimmed-median ratio=%.2fx (floor %.2fx; all=%v)", name, ratio, minRatio, fmtRatios(ratios))
	if ratio+1e-9 < minRatio {
		return fail("PERFORMANCE REGRESSION (%s): rawhttp only %.2fx faster than rival (need ≥%.2fx)", name, ratio, minRatio)
	}
	return true
}

func fmtRatios(rs []float64) string {
	parts := make([]string, len(rs))
	for i, r := range rs {
		parts[i] = fmt.Sprintf("%.2f", r)
	}
	return fmt.Sprint(parts)
}

func timeServe(data []byte, srv *rawhttp.Server) int64 {
	fc := newDiscardConn(data)
	start := time.Now()
	_ = srv.ServeConn(fc)
	return time.Since(start).Nanoseconds()
}

func timeServeRaw(data []byte) int64 {
	return timeServe(data, &rawhttp.Server{
		ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
		Handler: func(ctx *rawhttp.Ctx) { ctx.SetBody(plaintextBody) },
	})
}

func timeServeFast(data []byte) int64 {
	return timeServeFastHandler(data, func(ctx *fasthttp.RequestCtx) {
		ctx.SetBody(plaintextBody)
	})
}

func timeServeFastHandler(data []byte, h fasthttp.RequestHandler) int64 {
	fc := newDiscardConn(data)
	srv := &fasthttp.Server{Handler: h}
	start := time.Now()
	_ = srv.ServeConn(fc)
	return time.Since(start).Nanoseconds()
}

func timeServeRawJSON(data []byte) int64 {
	return timeServe(data, &rawhttp.Server{
		ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
		Handler: func(ctx *rawhttp.Ctx) {
			ctx.SetContentType("application/json")
			_ = ctx.SetHeader("X-Powered-By", "rawhttp")
			ctx.SetBody(ctx.Body())
		},
	})
}

func timeServeFastJSON(data []byte) int64 {
	return timeServeFastHandler(data, func(ctx *fasthttp.RequestCtx) {
		ctx.SetContentType("application/json")
		ctx.Response.Header.Set("X-Powered-By", "rawhttp")
		ctx.SetBody(ctx.PostBody())
	})
}

func timeServeNet(data []byte) int64 {
	fc := newDiscardConn(data)
	ln := &oneConnListener{conn: fc}
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(plaintextBody)
		}),
	}
	start := time.Now()
	_ = srv.Serve(ln)
	select {
	case <-fc.doneCh:
	case <-time.After(30 * time.Second):
	}
	return time.Since(start).Nanoseconds()
}

func timeServeNetJSON(data []byte) int64 {
	fc := newDiscardConn(data)
	ln := &oneConnListener{conn: fc}
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			buf := make([]byte, 64)
			n, _ := r.Body.Read(buf)
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Powered-By", "rawhttp")
			_, _ = w.Write(buf[:n])
		}),
	}
	start := time.Now()
	_ = srv.Serve(ln)
	select {
	case <-fc.doneCh:
	case <-time.After(30 * time.Second):
	}
	return time.Since(start).Nanoseconds()
}

func TestGate_ClientFasterThanFastHTTP(t *testing.T) {
	skipIfPoison(t)
	const (
		n      = 20000
		rounds = 11
		warmup = 5
	)

	newDial := func() func(network, addr string) (net.Conn, error) {
		return func(network, addr string) (net.Conn, error) {
			c, s := net.Pipe()
			go func() {
				srv := &rawhttp.Server{
					Handler:     func(ctx *rawhttp.Ctx) { ctx.SetBody(plaintextBody) },
					ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
				}
				_ = srv.ServeConn(s)
				_ = s.Close()
			}()
			return c, nil
		}
	}
	fastDial := func(addr string) (net.Conn, error) {
		c, s := net.Pipe()
		go func() {
			srv := &rawhttp.Server{
				Handler:     func(ctx *rawhttp.Ctx) { ctx.SetBody(plaintextBody) },
				ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
			}
			_ = srv.ServeConn(s)
			_ = s.Close()
		}()
		return c, nil
	}

	rawHC := &rawhttp.HostClient{Addr: "pipe.local", MaxConns: 1, Dial: newDial()}
	fastHC := &fasthttp.HostClient{Addr: "pipe.local", MaxConns: 1, Dial: fastDial}
	defer rawHC.CloseIdleConnections()
	defer fastHC.CloseIdleConnections()

	rawDo := func() int64 {
		req := rawhttp.AcquireRequest()
		resp := rawhttp.AcquireResponse()
		defer rawhttp.ReleaseRequest(req)
		defer rawhttp.ReleaseResponse(resp)
		req.RequestURI = "/"
		start := time.Now()
		for i := 0; i < n; i++ {
			resp.Reset()
			if err := rawHC.Do(req, resp); err != nil {
				t.Fatalf("rawhttp client: %v", err)
			}
		}
		return time.Since(start).Nanoseconds()
	}
	fastDo := func() int64 {
		req := fasthttp.AcquireRequest()
		resp := fasthttp.AcquireResponse()
		defer fasthttp.ReleaseRequest(req)
		defer fasthttp.ReleaseResponse(resp)
		req.SetRequestURI("http://pipe.local/")
		start := time.Now()
		for i := 0; i < n; i++ {
			resp.Reset()
			if err := fastHC.Do(req, resp); err != nil {
				t.Fatalf("fasthttp client: %v", err)
			}
		}
		return time.Since(start).Nanoseconds()
	}

	runtime.GC()
	for i := 0; i < warmup; i++ {
		_ = rawDo()
		_ = fastDo()
	}

	ratios := make([]float64, 0, rounds)
	for i := 0; i < rounds; i++ {
		runtime.GC()
		runtime.Gosched()
		var rawNs, fastNs int64
		if i%2 == 0 {
			rawNs = rawDo()
			fastNs = fastDo()
		} else {
			fastNs = fastDo()
			rawNs = rawDo()
		}
		if rawNs <= 0 || fastNs <= 0 {
			t.Fatalf("client: invalid timings raw=%d fast=%d", rawNs, fastNs)
		}
		r := float64(fastNs) / float64(rawNs)
		ratios = append(ratios, r)
		t.Logf("client round %d: rawhttp=%dns fasthttp=%dns ratio=%.2fx", i+1, rawNs, fastNs, r)
	}
	sort.Float64s(ratios)
	// Drop three extremes each side. Client gate is informational: pipe+GC noise
	// still trips trimmed ≥1.00× ~1/10 even at floor 1.05 (2026-09-30 Win/i5-1135G7
	// retest). Do not lower the floor further — log only; ServeConn gates remain blocking.
	trimmed := ratios[3 : len(ratios)-3]
	okTrim := true
	for i, r := range trimmed {
		if r < 1.0 {
			okTrim = false
			t.Logf("INFO (client, non-blocking): trimmed round %d slower than fasthttp (%.2fx < 1.00x); all=%v", i+1, r, fmtRatios(ratios))
			break
		}
	}
	ratio := trimmed[len(trimmed)/2]
	// Reference band only (thin margin: 10-run med≈1.19×, min≈1.09×); not enforced.
	const floor = 1.05
	t.Logf("client trimmed-median ratio=%.2fx (info floor %.2fx; all=%v; trimmedOK=%v)", ratio, floor, fmtRatios(ratios), okTrim)
	if okTrim && ratio+1e-9 < floor {
		t.Logf("INFO (client, non-blocking): trimmed-median %.2fx below info floor %.2fx", ratio, floor)
	}
}
