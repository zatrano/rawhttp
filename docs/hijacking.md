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

## Hijack ≠ WebSocket

```text
Hijack != WebSocket implementation
```

RawHTTP provides **only** low-level connection access via `Hijack`. There is **no** WebSocket helper, handshake API, or frame codec in this package.

**Important:** on the default HTTP path RawHTTP **rejects** requests that carry `Upgrade` or a `Connection: upgrade` token with **400 Bad Request** before the handler runs. A standards-shaped WebSocket handshake (`GET` + `Upgrade: websocket` + `Connection: Upgrade` + `Sec-WebSocket-*`) therefore **never reaches `Hijack`**.

`Hijack` is for **upgrade-free** custom protocols (or traffic already terminated/normalized upstream). Do not document or assume “WebSocket = Hijack + external library” against RawHTTP’s default parser: the handshake is rejected first.

If you need WebSockets in production, terminate/upgrade at a reverse proxy or use a stack that accepts the Upgrade handshake; RawHTTP’s Hijack path alone is not a WebSocket server.
