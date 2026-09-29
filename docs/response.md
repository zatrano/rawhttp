# Response

Responses are built on the same `*Ctx`. Default status is `200`.

## Status & headers

```go
ctx.SetStatusCode(201)
ctx.SetContentType("application/json")
_ = ctx.SetHeader("X-Request-ID", id) // rejects CR/LF / bad names
ctx.AddHeader("X-Extra", "a")
ctx.DelHeader("X-Extra")
ctx.SetETag(`"abc"`)
ctx.SetLastModified(t)
ctx.SetConnectionClose()
```

`SetHeader` returns `ErrHeaderInvalid` / related errors when values are unsafe.

## Body

```go
ctx.SetBody([]byte("hi"))
ctx.SetBodyString("hi")
ctx.AppendBody([]byte("!"))
ctx.Write([]byte("more")) // io.Writer-style
ctx.SetChunked()          // Transfer-Encoding: chunked
```

`ResponseBody()` returns the buffered response body being built.

## JSON / HTML / plain

```go
_ = ctx.WriteJSON(map[string]string{"ok": "true"})
ctx.SetContentType("text/html; charset=utf-8")
ctx.SetBodyString("<h1>hi</h1>")
```

## Shortcuts

| Method | Effect |
|--------|--------|
| `NotFound` | 404 |
| `BadRequest` | 400 |
| `Forbidden` | 403 |
| `Unauthorized` | 401 |
| `TooManyRequests` | 429 |
| `MethodNotAllowed` | 405 |
| `NotModified` | 304 |
| `Success` / `SuccessString` | 200 + body |
| `Error(msg, code)` | status + plain body |
| `Redirect(url, code)` | Location |
| `IfModifiedSince` | conditional GET helper |

## Streaming

```go
import "bufio"

ctx.SetBodyStream(reader, size) // size < 0 → chunked
ctx.SetBodyStreamWriter(func(w *bufio.Writer) {
	_, _ = w.Write([]byte("part"))
})
```

`StreamWriter` is the callback type `func(w *bufio.Writer)`, not a writer struct.

See [streaming.md](streaming.md).

## Files

```go
ctx.SendFile("/var/www/index.html") // no return value
// or rawhttp.ServeFile(ctx, path)
```

Static trees: `rawhttp.NewFS(...)` — see [files.md](files.md).

## After hijack

If `Hijack` succeeds, RawHTTP does not write the HTTP response. See [hijacking.md](hijacking.md).
