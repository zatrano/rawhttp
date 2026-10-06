# Body

## Buffered body (default)

```go
b := ctx.Body()
n := ctx.ContentLength()
```

Limited by `Server.MaxRequestBodySize`. Oversized → `413` / `ErrBodyTooLarge`.

## Expect: 100-continue

The effective cap (`Server.MaxRequestBodySize`, or `RequestConfig.MaxRequestBodySize` from `HeaderReceived`) is checked **before** `100 Continue`.

- `Content-Length` greater than the cap: `413`, `Connection: close`, and **no** `100 Continue`. The handler does not run. The connection goroutine then half-closes if it can, discards at most 256 KiB of the unread body (`LingerDrain`), and closes after at most 1s (`LingerTimeout`). Hitting the byte cap before the timeout still waits out the remaining time: closing earlier aborts the socket when unread data is buffered and the client loses the `413`. A client that is still writing can read the status until that timeout. `MaxLingering` (default 1024; zero uses that default; negative is unlimited) caps how many connections may do this at once. Past the cap the discard is skipped and the connection closes immediately. `Shutdown` and `Close` interrupt a discard already in progress.
- No `Content-Length` (chunked) with `Expect: 100-continue`: `100 Continue` is sent, then the cap is applied while the body is read. Crossing the cap yields `413`.
- `ContinueHandler` returning false still rejects with `417` and does not send `100`. That check runs before the length check.

`HeaderReceived` returning `RequestConfig.RejectStatus` in 400–599 is decided even earlier: a short response is written, the body is not read, the handler does not run, and the connection closes (`Connection: close`). Bytes already buffered for a pipelined next request are not parsed. `RejectStatus` `0` keeps the previous behavior. `RejectRetryAfter` (seconds), when positive, sets `Retry-After`. Statuses 413 and 429 without a retry delay reuse the standard static responses.

## Streaming request body

Server-wide:

```go
s := &rawhttp.Server{
	StreamRequestBody: true,
	Handler: func(ctx *rawhttp.Ctx) {
		r := ctx.RequestBodyStream()
		// read r; unread remainder is drained so the connection can stay keep-alive
	},
}
```

Per request, `RequestConfig.StreamBody: true` streams that request even when `Server.StreamRequestBody` is false. `StreamBody: false` does **not** disable a server-wide stream. The body cap is applied while the stream is read. If a per-request stream is not fully consumed, the response is written and the connection is closed instead of draining the rest for keep-alive.

A chunked body larger than the connection read buffer is read up to the body cap (`413`). It is not rejected as `431` merely because the header buffer filled after the headers were released.

## Response body

See [response.md](response.md): `SetBody`, `AppendBody`, `SetBodyStream`, `SetBodyStreamWriter`, `SetChunked`.

## ReduceMemoryUsage

When `Server.ReduceMemoryUsage` is true, large request body buffers may be dropped after each request so capacity is not retained across keep-alive.
