# Production

## Checklist

1. Set explicit `ReadTimeout` / `WriteTimeout` / `IdleTimeout`.
2. Cap `MaxRequestBodySize`, `MaxHeaderBytes`, `MaxHeaders`, `Concurrency`.
3. Prefer `AllowedHosts` / `AllowedMethods` / `GetOnly` on untrusted edges.
4. Use `TrustedProxies` only for known reverse-proxy CIDRs before trusting `ClientIP`.
5. Enable `DisablePipelining` on hostile edges if needed.
6. TLS via `ListenAndServeTLS` or terminate TLS at a proxy and set `RequireTLS` appropriately.
7. Graceful stop: `Shutdown(ctx)`.
8. Optional: `ListenReusePort`, `Prefork` for multi-core listen scaling.
9. Observe `OpenConnections` / `TotalRequests`; hook `ConnState` / `ErrorHandler`.
10. Read [SECURITY.md](../SECURITY.md) — v0.1 is experimental.

## Proxy / load balancer

Place RawHTTP behind nginx/envoy/etc. Forwarded headers are honored for `ClientIP` only when the peer matches `TrustedProxies`.

## Client in production

Tune `MaxConnsPerHost`, idle durations, `MaxResponseBodySize`, TLS config, and dial (`TCPDialer` / `HTTPProxyDial` / `SOCKS5ProxyDial`).
