# Streaming

## Response stream

```go
import "bufio"

// Fixed length or chunked (size < 0)
ctx.SetBodyStream(reader, size)

// StreamWriter is func(w *bufio.Writer) — pass a *bufio.Writer callback
ctx.SetBodyStreamWriter(func(w *bufio.Writer) {
	_, _ = w.Write([]byte("chunk-a"))
	_, _ = w.Write([]byte("chunk-b"))
})
```

`NewStreamReader` builds an `io.Reader` from a `StreamWriter` callback. `IsBodyStream()` reports whether the response uses a stream.

`Server.WriteBufferSize` is wired into stream response writes when set.

## Request stream

`Server.StreamRequestBody = true` then `ctx.RequestBodyStream()`.

## Client

`Client` / `HostClient` field `StreamResponseBody`: response body available as `Response.BodyStream()`; call `CloseBodyStream()` when done.
