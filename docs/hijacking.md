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

- custom binary protocols after an HTTP request
- hand-rolled upgrade-style protocols implemented **outside** RawHTTP
- debugging / connection inspection

## Hijack ≠ WebSocket

```text
Hijack != WebSocket implementation
```

RawHTTP provides **only** low-level connection access via `Hijack`. There is **no** WebSocket helper, handshake API, or frame codec in this package.

If you need WebSockets, use a separate library on top of the hijacked `net.Conn` (and handle buffering / leftover yourself).

On the normal HTTP path RawHTTP rejects `Connection: upgrade` for security; that is not a WebSocket stack.
