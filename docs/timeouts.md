# Timeouts

## Server

| Field | Role |
|-------|------|
| `ReadHeaderTimeout` | Time to read headers after the first byte of the request |
| `ReadTimeout` | Read deadline covering the body (and default for header phase when `ReadHeaderTimeout` is zero) |
| `WriteTimeout` | Write deadline |
| `IdleTimeout` | Keep-alive idle wait before the next request’s first byte |
| `ContinueTimeout` | Expect: 100-continue |
| `ConcurrencyWaitTimeout` | Wait for concurrency slot |
| `MaxConnDuration` | Absolute conn lifetime |

| Field | Zero | Negative | Default |
|-------|------|----------|---------|
| `ReadTimeout` | package default | unlimited | 30s |
| `WriteTimeout` | package default | unlimited | 30s |
| `IdleTimeout` | package default | unlimited | 90s |
| `ReadHeaderTimeout` | uses `ReadTimeout` | unlimited | (= ReadTimeout) |

Zero values select package defaults. Negative values disable deadlines where the implementation allows (used in benches/tests).

Per-request overrides via `HeaderReceived` → `RequestConfig{ReadTimeout, MaxRequestBodySize}`.

Full limit table (body/headers/concurrency) and **per-connection memory** (8 KiB read buffer; default no write buffer; `Ctx` pool scratch 512+256+512 B): [Production](production.md).

## Handler timeout

```go
h := rawhttp.TimeoutHandler(inner, 2*time.Second, "deadline")
// or TimeoutMiddleware
```

## Client

`DialTimeout`, `ReadTimeout`, `WriteTimeout`, `DoTimeout` / `DoDeadline`, `Request.SetTimeout`.
