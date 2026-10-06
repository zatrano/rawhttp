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
| `AllowUpgrade` | Opt-in: admit a standards-shaped WebSocket handshake to the handler (default **false** → 400). No frame codec; see [hijacking.md](hijacking.md) |
| `ReduceMemoryUsage` | Drop large body buffers after request |
| `ErrorHandler` / `ErrorCallback` / `ErrorLog` | Errors / panics |
| `ContinueHandler` / `HeaderReceived` | 100-continue / per-request limits (`RejectStatus`, `StreamBody`) |
| `LingerDrain` / `LingerTimeout` | After an early error response, discard at most this many bytes for at most this long (default 256 KiB / 1s), then close. The byte cap does not end the wait early. `Shutdown` / `Close` interrupt the wait |
| `MaxLingering` | How many connections may be in that discard at once. Zero uses 1024. Negative means unlimited. When the cap is full the discard is skipped and the connection closes immediately |
| `MaxConnsPerIP` / `MaxRequestsPerConn` / `MaxConnDuration` | Abuse controls |
| `TCPKeepalive` / `TCPKeepalivePeriod` | TCP options |
| `ConnState` | Lifecycle hook |
| `Name` | `Server` response header (empty → omit) |
| `NoDefaultDate` / `NoDefaultContentType` | Default header policy |

Stats (atomics): `OpenConnections`, `TotalConnections`, `TotalRequests`, `Lingering`.

## ServeConn

`ServeConn` runs the HTTP state machine on one `net.Conn`. Used heavily in tests and for custom accept loops.

## Related

- [Timeouts](timeouts.md)
- [Production](production.md)
- [Concurrency](concurrency.md)
