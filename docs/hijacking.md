# Hijacking

## What Hijack is

`Ctx.Hijack` transfers ownership of the underlying `net.Conn` from RawHTTP to the caller.

```go
conn, leftover, err := ctx.Hijack() // (net.Conn, []byte, error)
if err != nil {
	return
}
// leftover: unread buffered bytes (e.g. pipelined data) — process before reading conn
_, _ = conn.Write([]byte("custom-protocol"))
_ = conn.Close()
```

Errors: `ErrHijacked` (already hijacked), `ErrNotHijackable` (no connection available).

`Hijacked()` reports whether Hijack already succeeded for this request.

## Leaving the normal HTTP lifecycle

Normal path:

```text
accept → parse request → Handler → RawHTTP writes HTTP response → keep-alive or close
```

After a successful `Hijack`:

```text
accept → parse request → Handler calls Hijack → RawHTTP does NOT write an HTTP response
                                              → caller owns the connection bytes
```

Control passes to the user **when `Hijack` returns successfully**. From that point RawHTTP will not serialize status line, headers, or body for that request.

The caller is responsible for:

- consuming `leftover` before further reads
- applying deadlines / TLS / framing as needed
- closing the connection when done (unless the accept loop will close it — see below)

## KeepHijackedConns

By default the accept loop still **Closes** the connection when the serve goroutine ends — even after Hijack.

```go
s := &rawhttp.Server{KeepHijackedConns: true, Handler: h}
```

When `KeepHijackedConns` is true, the hijacked conn stays open for the caller to manage.

## HijackSetNoResponse

`HijackSetNoResponse` exists for fasthttp API parity. RawHTTP **never** writes an HTTP response after Hijack regardless of this flag.

## TimeoutError / TimeoutHandler

`TimeoutHandler` / `TimeoutError` support patterns that outlive the normal write path (e.g. background work with a retained Ctx). That is separate from Hijack; see `hijack.go`.

## Use cases

Typical uses of the low-level connection:

- custom binary protocols after a normal HTTP request (**without** `Upgrade`)
- debugging / connection inspection

## Hijack ≠ WebSocket (default)

```text
Hijack != WebSocket implementation
```

RawHTTP provides **only** low-level connection access via `Hijack`. There is **no** frame codec in this package.

**Default (`Server.AllowUpgrade == false`):** requests with `Upgrade` or a `Connection` upgrade token get **400** before the handler. A standards-shaped WebSocket handshake never reaches `Hijack`.

**Opt-in (`Server.AllowUpgrade == true`):** a handshake is admitted to the handler only when **all** of these hold:

- method `GET`, HTTP/1.1
- `Connection` token list contains `upgrade` (e.g. Firefox `keep-alive, Upgrade`)
- `Upgrade` is exactly the single token `websocket` (case-insensitive)
- no `Content-Length` / `Transfer-Encoding`
- `Sec-WebSocket-Version: 13`
- `Sec-WebSocket-Key` base64-decodes to **16** bytes

The handler must complete the 101 response and framing itself after `Hijack`. `leftover` carries any bytes already buffered past the headers (e.g. a frame sent in the same TCP write as the handshake).

### Origin / CSWSH

RawHTTP does **not** validate `Origin`. Cross-Site WebSocket Hijacking prevention is the **application's** responsibility:

```go
s.AllowUpgrade = true
s.Handler = func(ctx *rawhttp.Ctx) {
	origin := string(ctx.Header("Origin"))
	if origin != "https://example.com" {
		ctx.SetStatusCode(403)
		return
	}
	conn, leftover, err := ctx.Hijack()
	// … 101 + frames; process leftover first
	_, _, _ = conn, leftover, err
}
```

Enabling `AllowUpgrade` expands the attack surface: untrusted clients can reach your Hijack handler with a valid-looking handshake. Keep the default (`false`) unless you implement Origin checks, authentication, and framing carefully.

### Concurrency, MaxConnsPerIP, Shutdown

After `Hijack`, when the handler **returns**, the accept-loop goroutine ends and:

- the `Concurrency` slot is released
- `MaxConnsPerIP` is decremented
- `OpenConnections` decreases

With `KeepHijackedConns: true` the TCP conn stays open for the caller, but it **no longer counts** toward those limits. `Shutdown` waits only for accept-loop goroutines (`activeConn`); it does **not** wait on or close KeepHijacked connections after the handler returns (similar to `net/http`: the hijacker owns the conn). `Close` force-closes tracked conns still in the map; a KeepHijacked conn already removed from tracking is not closed by `Shutdown`.

### Hijack edilmiş bağlantı sayımı

`Concurrency` / `MaxConnsPerIP` yalnızca accept-loop yaşamına bağlıdır. Handler `Hijack` sonrası **döner dönmez** slot serbest kalır; uzun ömürlü WebSocket oturumları bu sayaçlarda görünmez.

WebSocket sayısını sınırlamak için:

1. **Handler içinde tut** — frame döngüsünü handler return etmeden çalıştır (slot meşgul kalır; `KeepHijackedConns` gerekmez), veya
2. **Uygulama limteri** — handler hızlı dönüyorsa kendi semaphor’unuzla sınırlayın:

```go
var wsSem = make(chan struct{}, 64) // max 64 concurrent WS

s.AllowUpgrade = true
s.KeepHijackedConns = true
s.Handler = func(ctx *rawhttp.Ctx) {
	select {
	case wsSem <- struct{}{}:
	default:
		ctx.SetStatusCode(503)
		return
	}
	conn, leftover, err := ctx.Hijack()
	if err != nil {
		<-wsSem
		return
	}
	go func() {
		defer func() { <-wsSem }()
		defer conn.Close()
		// process leftover, then frames…
		_, _ = leftover, conn
	}()
}
```
