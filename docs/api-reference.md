# API reference

Public surface of `github.com/zatrano/rawhttp` (v0.2.2). Signatures are summarized; see GoDoc / source for full field lists and edge cases.

There is **no** path-parameter router, **no** `DisableHeaderNamesNormalizing`, and **no** WebSocket frame codec in this package. Default parsing **rejects** `Upgrade` / `Connection: upgrade` with 400. Set `Server.AllowUpgrade` to admit a standards-shaped handshake to the handler for `Hijack` (see [Hijacking](hijacking.md)).

## Package entry

| Name | Kind | Purpose | Signature (summary) |
|------|------|---------|---------------------|
| `Handler` | type | Request handler | `func(*Ctx)` |
| `ListenAndServe` | func | Listen TCP + serve | `(addr string, h Handler) error` |
| `ListenAndServeTLS` | func | TLS listen + serve | `(addr, certFile, keyFile string, h Handler) error` |
| `ListenReusePort` | func | `SO_REUSEPORT` listen | `(network, address string) (net.Listener, error)` |
| `Prefork` / `PreforkIsChild` | func | Multi-process serve | `Prefork(s *Server, cfg PreforkConfig) error` |
| `AdaptHandler` / `AdaptHandlerFunc` | func | Adapt toward `net/http` | `AdaptHandler(http.Handler) Handler` |
| `StatusText` | func | Status text for code | `(code int) string` |
| `BodyLimitConfig` | func | Helper for `HeaderReceived` | `(n int) func(*Ctx) RequestConfig` |
| `ServeFile` | func | Serve one file path | `(ctx *Ctx, path string)` |
| `NewFS` | func | Construct `FS` | `(root string) *FS` |
| `NewStreamReader` | func | `StreamWriter` → `io.Reader` | `(sw StreamWriter) io.ReadCloser` |
| `Dial` / `DialTimeout` / `DialDualStack` / `DialDualStackTimeout` | func | Package TCP dial + DNS cache | see `TCPDialer` |
| `HTTPProxyDial` / `SOCKS5ProxyDial` (+ Timeout variants) | func | Proxy dialers | `(proxyAddr string) DialFunc` |
| `AcquireRequest` / `ReleaseRequest` | func | Client request pool | |
| `AcquireResponse` / `ReleaseResponse` | func | Client response pool | |
| `HealthCheck` / `ReadyCheck` | func | Probe handlers | `ReadyCheck(ready func() bool) Handler` |
| `DecodeFormPlus` | func | `+` → space in-place | `(b []byte)` |
| `OpenMultipartFile` / `ReadMultipartFile` / `SaveMultipartFile` | func | Multipart file I/O | see forms/files docs |
| `TimeoutHandler` / `TimeoutMiddleware` | func | Handler deadline → 503 | `(h Handler, timeout time.Duration, msg string) Handler` |
| `NetHTTPFormValueFunc` | var | FormValue order like net/http | `FormValueFunc` |

**Basic usage:**

```go
log.Fatal(rawhttp.ListenAndServe(":8080", func(ctx *rawhttp.Ctx) {
	ctx.SetBodyString("ok")
}))
```

## Server

**Type** `Server` — fields in [server.md](server.md) (includes `AllowUpgrade`, `KeepHijackedConns`, timeouts, limits).

**Methods:** `ListenAndServe`, `ListenAndServeTLS`, `Serve`, `ServeConn`, `Shutdown`, `Close`.

```go
s := &rawhttp.Server{
	Handler:      h,
	ReadTimeout:  30 * time.Second,
	AllowUpgrade: false, // default; set true only with Origin/auth + framing
}
_ = s.ListenAndServe(":8080")
```

**Related types:**

| Type | Purpose |
|------|---------|
| `RequestConfig` | Per-request overrides from `HeaderReceived` |
| `ConnState` | `StateNew`, `StateActive`, `StateIdle`, `StateClosed` |
| `FormValueFunc` | `func(ctx *Ctx, key string) []byte` |
| `PreforkConfig` | Prefork worker settings |
## Ctx

Exported fields: `Method`, `Path`, `Query`, `StatusCode`.

Request/response methods: [request.md](request.md), [response.md](response.md), [headers.md](headers.md), [cookies.md](cookies.md), [body.md](body.md), [hijacking.md](hijacking.md).

Notable methods:

| Method | Purpose |
|--------|---------|
| `Hijack` | `(conn net.Conn, leftover []byte, err error)` — take connection |
| `Hijacked` | whether Hijack was called |
| `HijackSetNoResponse` | API compatibility; RawHTTP never writes after Hijack |
| `TimeoutError` | mark 503 timeout (retained-Ctx pattern) |
| `URI` | `*URI` view of the request target |
| `QueryArgs` / `PostArgs` | `QueryArgs` views |
| `MultipartForm` | `(*MultipartForm, error)` |
| `SendFile` | `(path string)` — no return value |

## Query / URI helpers

| Type | Purpose |
|------|---------|
| `QueryArgs` | Read-only view over raw query bytes (`Get`, `GetString`, `Has`, `Visit`, …) |
| `Args` | Mutable query/form buffer (`Parse`, `Set`, `Add`, `Del`, `QueryString`, …) |
| `URI` | Parsed target (`Scheme`, `Host`, `Path`, `QueryString`, `Hash`, credentials, …) |

```go
page := ctx.QueryArgs().GetString("page")
u := ctx.URI()
_ = string(u.Path())
```

## Cookies

| Type / const | Purpose |
|--------------|---------|
| `Cookie` | Cookie attributes for `SetCookie` |
| `SameSite` | `SameSiteDefaultMode`, `SameSiteLaxMode`, `SameSiteStrictMode`, `SameSiteNoneMode` |

```go
_ = ctx.SetCookie(&rawhttp.Cookie{Name: "sid", Value: "1", SameSite: rawhttp.SameSiteLaxMode})
_ = ctx.SecureCookie("sid", "1") // name, value only
```

## Forms / multipart

| Type | Purpose |
|------|---------|
| `MultipartForm` | `Value map[string][]string`, `File map[string][]*multipart.FileHeader` |

See [forms.md](forms.md).

## Streaming

| Type | Purpose |
|------|---------|
| `StreamWriter` | `func(w *bufio.Writer)` — callback type for streaming bodies |

```go
import "bufio"

ctx.SetBodyStreamWriter(func(w *bufio.Writer) {
	_, _ = w.WriteString("part")
})
```

## Files

**Type** `FS` — static file server (`NewFS`, `Handler()`). See [files.md](files.md).

## Client

**Types:** `Client`, `HostClient`, `PipelineClient`, `LBClient`, `Request`, `Response`, `RetryIfErrFunc`, `ConnPoolStrategyType` (`LIFO`, `FIFO`).

**Methods (Client):** `Do` / `DoTimeout` / `DoDeadline`, `Get` / `GetTimeout`, `Post` / `PostForm` / `Put` / `Patch` / `Delete`, idle-connection cleanup where applicable.

**HostClient:** `Do*`, `PendingRequests`, `CloseIdleConnections`, …

```go
c := &rawhttp.Client{}
resp := rawhttp.AcquireResponse()
defer rawhttp.ReleaseResponse(resp)
_ = c.Get("http://127.0.0.1:8080/", resp)
```

## Dial / proxy

| Name | Purpose |
|------|---------|
| `TCPDialer` | Dialer with DNS cache (`Concurrency`, `DNSCacheDuration`, `Resolver`, …) |
| `Resolver` | `LookupIPAddr(ctx, host) ([]net.IPAddr, error)` |
| `DefaultDialTimeout` | `3s` |
| `DefaultDNSCacheDuration` | `1m` |
| `Dial` / `DialTimeout` / `DialDualStack` / `DialDualStackTimeout` | Package helpers on default dialer |
| `HTTPProxyDial` / `HTTPProxyDialTimeout` | HTTP CONNECT |
| `SOCKS5ProxyDial` / `SOCKS5ProxyDialTimeout` | SOCKS5 |

## Middleware

See [middleware.md](middleware.md). Built-ins take `Handler` and return `Handler`.

| Name | Notes |
|------|-------|
| `RecoverMiddleware` | `(h Handler, onPanic func(any)) Handler` — pass `nil` for onPanic |
| `AccessLogFunc` | `func(ctx *Ctx, status int, dur time.Duration)` |
| `RequestIDOptions`, `CompressOptions`, `CORSOptions`, `SecureHeadersOptions`, `RateLimitOptions` | Option structs |

```go
h := rawhttp.RecoverMiddleware(app, nil)
h = rawhttp.RequestIDMiddleware(h, rawhttp.RequestIDOptions{})
```

## Errors

Exported sentinels: `ErrBadRequest`, `ErrBodyTooLarge`, `ErrURITooLong`, `ErrHeaderInvalid`, `ErrHeaderTooLarge`, `ErrServerClosed`, `ErrHijacked`, `ErrNotHijackable`, `ErrMisdirectedRequest`, `ErrRedirect`, `ErrTimeout`, `ErrNoFreeConns`, `ErrMissingLocation`, `ErrPerIPConnLimit`, `ErrConnPoolStrategyNotImpl`.

See [errors.md](errors.md).

## Thread-safety

- `Server` methods: do not mutate config after Serve starts.
- `Ctx`: request-scoped; do not retain buffer slices after the handler returns. Debug: `-tags rawhttp_poison` fills retained request bytes with `0xDE` after the handler (see [concepts](concepts.md)).
- Pooled client `Request` / `Response`: exclusive until released.
