# Errors

## Sentinel errors (package)

| Error | Typical cause |
|-------|----------------|
| `ErrBadRequest` | Malformed request / validation |
| `ErrBodyTooLarge` | Body over limit |
| `ErrURITooLong` | URI over `MaxURILength` |
| `ErrHeaderInvalid` | Unsafe response header |
| `ErrHeaderTooLarge` | Headers over limit |
| `ErrServerClosed` | Serve after shutdown |
| `ErrHijacked` / `ErrNotHijackable` | Hijack state |
| `ErrMisdirectedRequest` | Host / TLS routing policy |
| `ErrRedirect` | Client redirect policy |
| `ErrTimeout` | Client deadline |
| `ErrNoFreeConns` | Client pool exhausted |
| `ErrMissingLocation` | Redirect without Location |
| `ErrPerIPConnLimit` | `MaxConnsPerIP` |
| `ErrConnPoolStrategyNotImpl` | Bad pool strategy |

## Server hooks

```go
s := &rawhttp.Server{
	ErrorCallback: func(err error) { /* accept/parse errors */ },
	ErrorHandler:  func(ctx *rawhttp.Ctx, err error) { /* ctx may be nil */ },
	ErrorLog:      log.Default(),
}
```

Panics in handlers are recovered by default (`DisablePanicRecovery` to turn off) → `500`.

## Handler-level

Prefer `ctx.BadRequest()`, `ctx.Error(msg, code)`, etc. for application errors.
