# rawhttp security baseline

The baseline is what a zero-value `rawhttp.Server` does before the operator
changes anything. Each row is enforced in code and checked by the test named
in [SECURITY-GUARANTEES.md](SECURITY-GUARANTEES.md).

## Default limits

| Limit | Field | Default | Over the limit |
|-------|-------|---------|----------------|
| Header block (request line included) | `MaxHeaderBytes` | 8192 bytes | 431, close |
| Header lines | `MaxHeaders` | 100 | 400, close |
| Header name length | (fixed) | 256 bytes | 400, close |
| Method length | (fixed) | 32 bytes | 400, close |
| Request body (Content-Length or chunked total) | `MaxRequestBodySize` | 4 MiB | 413, close |
| Chunk frames per body | `MaxChunks` | 16384 | 400, close |
| Chunk-size width | (fixed) | 16 hex digits | 400, close |
| Chunk extension | (fixed) | 256 bytes, no CTL | 400, close |
| Trailer lines (streamed body) | (fixed) | 100 | 400, close |
| Read (headers and body) | `ReadTimeout` | 30 s | close |
| Header read after first byte | `ReadHeaderTimeout` | = `ReadTimeout` | close |
| Write | `WriteTimeout` | 30 s | close |
| Idle keep-alive | `IdleTimeout` | 90 s | close |
| Open connections | `Concurrency` | 262144 | accept waits (`ConcurrencyWaitTimeout`) |
| Requests on one connection | `MaxRequestsPerConn` | 1_000_000 | close after the Nth response |
| Consecutive pipelined requests | `MaxPipelineDepth` | 64 | close after that many; `DisablePipelining` is stricter |
| Body read rate after 1 s | `MinBodyBytesPerSecond` | 64 | 408, close |
| Discard after an early error | `LingerDrain` / `LingerTimeout` | 256 KiB / 1 s | close |
| Connections discarding at once | `MaxLingering` | 1024 | close without discard |
| Multipart memory / files / parts | `MaxMultipartMemory` / `MaxMultipartFiles` / `MaxMultipartParts` | 16 MiB / 32 / 128 | error to the handler |

Off by default; set them for your traffic:

| Field | Default | Effect when set |
|-------|---------|-----------------|
| `MaxURILength` | 0 (header budget only) | 414 above the value |
| `MaxConnsPerIP` | 0 (none) | 429 and close above the value |
| `MaxConnDuration` | 0 (none) | close after the duration |
| `DisablePipelining` | false | close when a second request is already buffered |
| `AllowedHosts` | empty (any Host) | 421 for other hosts |
| `TrustedProxies` | empty (`ClientIP` = peer) | forwarded headers read from these peers only |

Negative `MaxRequestsPerConn`, `MaxPipelineDepth`, or `MinBodyBytesPerSecond` turns that limit off.

## Default behaviour

- HTTP/1.0 and HTTP/1.1 only. Every framing rule in R1 is always on; none has
  a switch.
- `Upgrade` and `Connection: upgrade` are rejected with 400 unless
  `AllowUpgrade` is set; then only a well-formed WebSocket handshake reaches the
  handler.
- Handler panics are recovered and answered with a closing 500 unless
  `DisablePanicRecovery` is set.
- Response headers: CR, LF and CTL bytes are rejected, `Content-Length` and
  `Transfer-Encoding` are engine-only, `Set-Cookie` values cannot carry `;`.
- `ClientIP` returns the TCP peer unless the peer is in `TrustedProxies`. With
  a trusted peer it walks `X-Forwarded-For` from the right and returns the
  first address that is not a trusted proxy.
- TLS through `ListenAndServeTLS` or `ServerTLSConfig`: TLS 1.2 minimum,
  ECDHE with AES-GCM or ChaCha20-Poly1305 on TLS 1.2.

## Recommended production values

These are starting points, not measured optima. Lower them when the
application allows.

```go
srv := &rawhttp.Server{
	Handler:            h,
	ReadHeaderTimeout:  5 * time.Second,
	ReadTimeout:        30 * time.Second,
	WriteTimeout:       30 * time.Second,
	IdleTimeout:        60 * time.Second,
	MaxRequestBodySize: 1 << 20,
	MaxURILength:       4096,
	MaxConnsPerIP:      256,
	TrustedProxies:     []string{"10.0.0.0/8"}, // only the proxies you run
}
```

See [DEPLOYMENT-HARDENING.md](DEPLOYMENT-HARDENING.md) for the host side.
