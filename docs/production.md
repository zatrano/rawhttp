# Production

## Checklist

1. Set explicit `ReadTimeout` / `WriteTimeout` / `IdleTimeout` (and usually `ReadHeaderTimeout`).
2. Cap `MaxRequestBodySize`, `MaxHeaderBytes`, `MaxHeaders`, `Concurrency`.
3. Prefer `AllowedHosts` / `AllowedMethods` / `GetOnly` on untrusted edges.
4. Use `TrustedProxies` only for known reverse-proxy CIDRs before trusting `ClientIP`.
5. Enable `DisablePipelining` on hostile edges if needed.
6. TLS via `ListenAndServeTLS` or terminate TLS at a proxy and set `RequireTLS` appropriately.
7. Graceful stop: `Shutdown(ctx)`.
8. Optional: `ListenReusePort`, `Prefork` for multi-core listen scaling.
9. Observe `OpenConnections` / `TotalRequests`; hook `ConnState` / `ErrorHandler`.
10. Read [SECURITY.md](../SECURITY.md) — v0.2.2 is GA for application embedding (not a reverse-proxy claim).
11. If you enable `AllowUpgrade`, enforce Origin / auth and either keep the WS loop inside the handler or use an application-level limiter (Hijack releases `Concurrency` / `MaxConnsPerIP` when the handler returns — see [hijacking.md](hijacking.md)).
12. Downstream integration tests: prefer `-tags rawhttp_poison` so accidental post-handler slice retention fails loudly.

## Default limits (code)

Zero means “use package default” unless noted. Negative timeouts disable deadlines where the implementation allows (benches/tests).

| Field | Zero | Negative | Default |
|-------|------|----------|---------|
| `ReadTimeout` | → default | unlimited | **30s** |
| `WriteTimeout` | → default | unlimited | **30s** |
| `IdleTimeout` | → default | unlimited | **90s** |
| `ReadHeaderTimeout` | → **ReadTimeout** | unlimited | (= ReadTimeout) |
| `MaxRequestBodySize` | → default | — | **4 MiB** |
| `MaxHeaderBytes` | → default | — | **8 KiB** |
| `MaxHeaders` | → default | — | **100** |
| `MaxChunks` | → default | — | **16384** |
| `Concurrency` | → default | unlimited | **262144** |
| `MaxRequestsPerConn` | **unlimited** | — | unlimited |
| `MaxConnsPerIP` | **off** | — | off |
| `DisablePipelining` | false | — | pipelining **allowed** |

See also [Timeouts](timeouts.md).

## Per-connection memory (defaults)

Steady-state cost per **accepted** connection (code-backed). Handler-owned body copies and multipart temp files are extra.

| Resource | Initial / default | Pool | Notes |
|----------|------------------:|------|-------|
| Read buffer (`connReader.buf`) | **8192 B** | `readerBufPool` (`doc.go`) | `ReadBufferSize` if set (≥128), else `max(MaxHeaderBytes, 8192)` via `connReadBufSize` |
| Write / stream buffer | **0** (direct `conn.Write`) | — | `WriteBufferSize == 0` (default): no per-conn write buffer |
| Stream `bufio.Writer` (optional) | sized to `WriteBufferSize` (pool default **4 KiB**) | `streamWriterPool` (`response.go`) | Only if `WriteBufferSize > 0`; borrowed for the stream write, not held idle |
| Stream copy scratch | **32 KiB** | `streamCopyBufPool` | Only while copying a stream body |
| `Ctx` (one per conn in `serveLoop`) | **~1280 B** scratch at pool `New` | `ctxPool` | `respBuf` cap **512**, `outBuf` cap **256**, `reqBody` cap **512** (`doc.go`); grows with large bodies / header copies; returned when the connection ends |

**Rule of thumb (defaults, idle keep-alive):** ≈ **8 KiB read buffer + one pooled `Ctx`** per open connection. Enabling `WriteBufferSize` or large request bodies increases that footprint. `Concurrency` (default 262144) is an admission cap, not a pre-allocation of that many buffers.

See also [Timeouts](timeouts.md).

## Proxy / load balancer

Place RawHTTP behind nginx/envoy/etc. Forwarded headers are honored for `ClientIP` only when the peer matches `TrustedProxies`.

## Client in production

Tune `MaxConnsPerHost`, idle durations, `MaxResponseBodySize`, TLS config, and dial (`TCPDialer` / `HTTPProxyDial` / `SOCKS5ProxyDial`).
