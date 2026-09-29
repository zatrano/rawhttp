# Forms

## URL-encoded

```go
name := string(ctx.PostArgs().Get("name"))
city := string(ctx.FormValue("city")) // query → post → multipart text
ctx.VisitForm(func(k, v []byte) {})
```

`FormValue` default order: query, then urlencoded body, then multipart text field.

For `net/http` precedence (body before query):

```go
s := &rawhttp.Server{
	FormValueFunc: rawhttp.NetHTTPFormValueFunc,
	Handler:       h,
}
```

Custom: `FormValueFunc func(ctx *Ctx, key string) []byte`.

`HasFormBody()` reports urlencoded form POSTs.

`DecodeFormPlus` replaces `+` with space in-place for form decoding.

## Multipart

```go
mf, err := ctx.MultipartForm(0) // 0 → default max memory
fh, err := ctx.FormFile("file")
val := ctx.MultipartValue("title")
```

Limits: `Server.MaxMultipartMemory`, `MaxMultipartFiles`, `MaxMultipartParts`.

Helpers: `OpenMultipartFile`, `ReadMultipartFile`, `SaveMultipartFile`.
