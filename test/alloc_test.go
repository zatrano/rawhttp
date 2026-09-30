package test_test

import (
	"testing"

	"github.com/zatrano/rawhttp"
)

// Allocation contract: plaintext keep-alive hello must stay at 0 allocs/op
// across a single ServeConn (same model as BenchmarkRawHTTP_Plaintext).
func TestAllocs_PlaintextHello(t *testing.T) {
	skipIfPoison(t)
	res := testing.Benchmark(func(b *testing.B) {
		data := buildRequests(b.N)
		fc := newDiscardConn(data)
		srv := &rawhttp.Server{
			ReadTimeout:  -1,
			WriteTimeout: -1,
			IdleTimeout:  -1,
			Handler:      func(ctx *rawhttp.Ctx) { ctx.SetBody(plaintextBody) },
		}
		b.ReportAllocs()
		b.ResetTimer()
		_ = srv.ServeConn(fc)
	})
	if res.AllocsPerOp() != 0 {
		t.Fatalf("plaintext hello allocs/op = %d; want 0 (ns/op=%d)", res.AllocsPerOp(), res.NsPerOp())
	}
}

func TestAllocs_JSONPost(t *testing.T) {
	skipIfPoison(t)
	res := testing.Benchmark(func(b *testing.B) {
		payload := []byte(`{"msg":"hello"}`)
		one := "POST /api HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/json\r\nContent-Length: 16\r\n\r\n" + string(payload)
		data := make([]byte, 0, len(one)*b.N)
		for i := 0; i < b.N; i++ {
			data = append(data, one...)
		}
		fc := newDiscardConn(data)
		srv := &rawhttp.Server{
			ReadTimeout:  -1,
			WriteTimeout: -1,
			IdleTimeout:  -1,
			Handler: func(ctx *rawhttp.Ctx) {
				ctx.SetContentType("application/json")
				_ = ctx.SetHeader("X-Powered-By", "rawhttp")
				ctx.SetBody(ctx.Body())
			},
		}
		b.ReportAllocs()
		b.ResetTimer()
		_ = srv.ServeConn(fc)
	})
	// Body path may copy into pooled buffers on first requests; keep a tight ceiling.
	if res.AllocsPerOp() > 2 {
		t.Fatalf("json post allocs/op = %d; want ≤2 (ns/op=%d)", res.AllocsPerOp(), res.NsPerOp())
	}
}
