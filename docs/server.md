# Server

## Entry points

```go
// Package helpers
rawhttp.ListenAndServe(":8080", handler)
rawhttp.ListenAndServeTLS(":8443", "cert.pem", "key.pem", handler)

// Explicit Server
s := &rawhttp.Server{Handler: handler}
_ = s.ListenAndServe(":8080")
_ = s.Serve(listener)
_ = s.ServeConn(conn) // tests / custom listeners
```

Shutdown:

```go
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()
_ = s.Shutdown(ctx) // or s.Close()
```

## Important configuration fields

| Field | Role |
|-------|------|
| `Handler` | Request handler |
| `ReadTimeout` / `WriteTimeout` / `IdleTimeout` / `ReadHeaderTimeout` | Deadlines (`0` → package defaults; negative disables where supported) |
| `MaxRequestBodySize` / `MaxHeaderBytes` / `MaxHeaders` / `MaxChunks` | Limits |
| `ReadBufferSize` / `WriteBufferSize` | I/O buffers |
| `Concurrency` | Max concurrent connections (default large) |
| `DisableKeepalive` / `DisablePipelining` | Connection policy |
| `GetOnly` / `AllowedMethods` / `AllowedHosts` / `RequireTLS` | Access policy |
| `TrustedProxies` | CIDRs for `ClientIP` / `X-Forwarded-For` |
| `StreamRequestBody` | Opt-in streaming request body |
| `DisablePathNormalizing` | Allow `..` segments (proxy use) |
| `FormValueFunc` | Custom `FormValue` order (`NetHTTPFormValueFunc`) |
| `KeepHijackedConns` | Do not Close after Hijack |
| `ReduceMemoryUsage` | Drop large body buffers after request |
| `ErrorHandler` / `ErrorCallback` / `ErrorLog` | Errors / panics |
| `ContinueHandler` / `HeaderReceived` | 100-continue / per-request limits |
| `MaxConnsPerIP` / `MaxRequestsPerConn` / `MaxConnDuration` | Abuse controls |
| `TCPKeepalive` / `TCPKeepalivePeriod` | TCP options |
| `ConnState` | Lifecycle hook |
| `Name` | `Server` response header (empty → omit) |
| `NoDefaultDate` / `NoDefaultContentType` | Default header policy |

Stats (atomics): `OpenConnections`, `TotalConnections`, `TotalRequests`.

## ServeConn

`ServeConn` runs the HTTP state machine on one `net.Conn`. Used heavily in tests and for custom accept loops.

## Related

- [Timeouts](timeouts.md)
- [Production](production.md)
- [Concurrency](concurrency.md)
