# Body

## Buffered body (default)

```go
b := ctx.Body()
n := ctx.ContentLength()
```

Limited by `Server.MaxRequestBodySize`. Oversized → `413` / `ErrBodyTooLarge`.

## Streaming request body

Opt-in:

```go
s := &rawhttp.Server{
	StreamRequestBody: true,
	Handler: func(ctx *rawhttp.Ctx) {
		r := ctx.RequestBodyStream()
		// read r; unread remainder is drained after handler
	},
}
```

## Response body

See [response.md](response.md): `SetBody`, `AppendBody`, `SetBodyStream`, `SetBodyStreamWriter`, `SetChunked`.

## ReduceMemoryUsage

When `Server.ReduceMemoryUsage` is true, large request body buffers may be dropped after each request so capacity is not retained across keep-alive.
