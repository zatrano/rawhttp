# Timeouts

## Server

| Field | Role |
|-------|------|
| `ReadHeaderTimeout` | Time to read headers |
| `ReadTimeout` | Read deadline covering body |
| `WriteTimeout` | Write deadline |
| `IdleTimeout` | Keep-alive idle wait |
| `ContinueTimeout` | Expect: 100-continue |
| `ConcurrencyWaitTimeout` | Wait for concurrency slot |
| `MaxConnDuration` | Absolute conn lifetime |

Zero values select package defaults. Negative values disable deadlines where the implementation allows (used in benches/tests).

Per-request overrides via `HeaderReceived` → `RequestConfig{ReadTimeout, MaxRequestBodySize}`.

## Handler timeout

```go
h := rawhttp.TimeoutHandler(inner, 2*time.Second, "deadline")
// or TimeoutMiddleware
```

## Client

`DialTimeout`, `ReadTimeout`, `WriteTimeout`, `DoTimeout` / `DoDeadline`, `Request.SetTimeout`.
