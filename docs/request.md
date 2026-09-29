# Request

All request access goes through `*rawhttp.Ctx` during the handler.

## Identity

| API | Meaning |
|-----|---------|
| `ctx.Method` | Method bytes (valid until handler returns) |
| `ctx.Path` | Path without query |
| `ctx.Query` | Raw query without `?` |
| `ctx.Host()` | Host |
| `ctx.RequestURI()` | Path + query form |
| `ctx.URI()` | Parsed `*URI` view |
| `ctx.IsHTTP11()` | HTTP/1.1 |
| `ctx.RemoteAddr()` / `RemoteIP()` / `ClientIP()` | Peer / trusted proxy IP |
| `ctx.LocalAddr()` | Local |
| `ctx.IsTLS()` / `TLSConnectionState()` | TLS |
| `ctx.Conn()` / `ConnID()` / `ConnRequestNum()` / `ConnTime()` / `Time()` / `ID()` | Connection / request meta |

## Headers

```go
v := ctx.Header("User-Agent") // or ctx.Peek
ctx.VisitHeader(func(k, v []byte) { /* ... */ })
```

Indexed helpers: `UserAgent`, `RequestContentType`, `Referer`, `Accept`, `AcceptEncoding`, `Authorization`, `Origin`.

See [headers.md](headers.md).

## Query

```go
qa := ctx.QueryArgs()
page := qa.GetString("page")
n, _ := qa.GetUint("n")
ok := qa.GetBool("debug")
qa.Visit(func(k, v []byte) {})
```

Mutable `Args` type also exists for building/parsing query strings (`Parse`, `Set`, `Add`, …).

## Body

```go
b := ctx.Body()           // buffered body (default)
n := ctx.ContentLength()
```

With `Server.StreamRequestBody`: `ctx.RequestBodyStream()` returns an `io.Reader`; unread data is drained after the handler.

See [body.md](body.md).

## Method / path helpers

`IsGet`, `IsHead`, `IsPost`, `IsPut`, `IsDelete`, `IsPatch`, `IsOptions`, `MethodEqual`, `PathEqual`, `PathHasPrefix`.

## Forms & cookies

See [forms.md](forms.md) and [cookies.md](cookies.md).

## JSON

```go
var in struct{ Name string `json:"name"` }
if err := ctx.ReadJSON(&in); err != nil {
	ctx.BadRequest()
	return
}
```

## User values

```go
ctx.SetUserValue("userID", id)
v := ctx.UserValue("userID")
ctx.VisitUserValues(func(k, v any) {})
```

## Lifetime

Do not store `Method`, `Path`, `Query`, or `Header` slices beyond the handler without copying. Pooled `Ctx` instances are reused.
