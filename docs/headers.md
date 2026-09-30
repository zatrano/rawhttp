# Headers

## Reading

```go
v := ctx.Header("Content-Type") // case-insensitive name match
v = ctx.Peek("Content-Type")    // alias
ctx.VisitHeader(func(key, val []byte) {
	// key/val valid until handler returns
})
```

Common headers are indexed at parse time for O(1) access (`Host`, `User-Agent`, `Content-Type`, …).

## Writing

```go
_ = ctx.SetHeader("X-Trace", "1")
ctx.AddHeader("X-Trace", "2")
ctx.DelHeader("X-Trace")
ctx.SetContentType("text/plain")
```

Unsafe names/values (CTL, CR/LF) are rejected → `ErrHeaderInvalid`.

## Normalization

Lookups match header names case-insensitively (common indexed headers + scan). That is the only public behavior.

There is **no** API to disable header-name normalizing (no `DisableHeaderNamesNormalizing` or equivalent).

Path normalization can be relaxed via `Server.DisablePathNormalizing` / client equivalents (for proxy-style `..` paths) — that is unrelated to header names.

## Security notes

- Response `SetHeader` blocks CR/LF to reduce response splitting.
- Request parsing rejects NUL, obs-fold, and illegal header-name whitespace.
- Duplicate `Host` / conflicting `Content-Length` are rejected at the protocol layer.

Details: [SECURITY.md](../SECURITY.md).
