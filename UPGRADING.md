# Upgrading

## Unreleased (from v1.0.1)

| Change | Before | After | What to do |
|--------|--------|-------|------------|
| `SetHeader("Content-Length", ...)`, `SetHeader("Transfer-Encoding", ...)` and the `SetHeaderBytes` forms | Header written next to the engine's own | `ErrHeaderInvalid`, nothing written | Remove the call. Use `SetBody`/`SetBodyStream(r, size)` for a known length; a negative size streams chunked. |
| `SetHeader("Connection", v)` | Header written as given | `close` token closes the connection; other values ignored | Use `SetConnectionClose()` or keep `SetHeader("Connection", "close")`. Drop `keep-alive`. |
| `SetCookie` with `;` in value, path or domain | Written | `ErrHeaderInvalid` | Encode the value (for example base64url). |
| HTTP/1.0 request with `Transfer-Encoding` | Body read as chunked | 400 and close | HTTP/1.0 clients must send `Content-Length`. |
| `ClientIP` with a trusted peer | Leftmost `X-Forwarded-For` hop | Rightmost hop that is not in `TrustedProxies` | List every proxy you run in `TrustedProxies`; a chain of proxies now needs all of them. |
| `ListenAndServeTLS` | Go defaults (TLS 1.2 minimum, Go's suite list) | `ServerTLSConfig`: TLS 1.2 minimum, ECDHE + AEAD only on TLS 1.2 | Clients that only speak CBC or RSA key exchange can no longer connect; pass your own config to `Serve(tls.NewListener(...))` if you must support them. |
| Handler panic | 500, connection could be reused | 500 with `Connection: close` | None. |
| `MaxRequestsPerConn` left at 0 | unlimited | 1_000_000 then close | Set `-1` for the old unlimited behaviour. |
| Pipelined requests | unlimited depth | 64 then close | Set `MaxPipelineDepth` to `-1` to remove the cap, or `DisablePipelining` to refuse pipelining. |
| Slow request body | `ReadTimeout` only | also 408 if slower than 64 bytes/s after 1 s | Set `MinBodyBytesPerSecond` to `-1` to disable the rate. |

## v1.0.0

No source changes are needed from v0.2.10: `go get github.com/zatrano/rawhttp@v1.0.0`. The import path stays `github.com/zatrano/rawhttp`.

## v0.2.9

A still-valid read or write deadline is not refreshed. The effective timeout is `[d − granule, d]`, granule = min(d/8, 1s). A kept deadline is earlier than `now+d` by at most one granule. It is never later than `d`.

The wait between keep-alive requests is `IdleTimeout` (90s when unset), not `ReadTimeout`. `ReadTimeout` covers a request body and the read once a request has started. An idle connection closes on `IdleTimeout`. A reader that does not drain the body is cut by `WriteTimeout`.
