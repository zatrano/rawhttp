# Middleware

## Model

```text
Request
   ↓
Middleware A
   ↓
Middleware B
   ↓
Handler
   ↓
Response
```

Each middleware is a function `Handler → Handler`:

```go
func logging(next rawhttp.Handler) rawhttp.Handler {
	return func(ctx *rawhttp.Ctx) {
		start := time.Now()
		next(ctx)
		log.Printf("%s %s %s", ctx.Method, ctx.Path, time.Since(start))
	}
}
```

Compose outward:

```go
h := rawhttp.Handler(app)
h = logging(h)
h = rawhttp.RecoverMiddleware(h, nil) // (Handler, onPanic func(any))
```

## Built-in helpers

| Function | Purpose |
|----------|---------|
| `RecoverMiddleware` | `(h, onPanic)` — panic → 500; `onPanic` may be `nil` |
| `RequestIDMiddleware` | Request ID header |
| `AccessLogMiddleware` | Access log callback |
| `BasicAuthMiddleware` | HTTP Basic |
| `CompressMiddleware` | Response compression |
| `CORSMiddleware` | CORS |
| `SecureHeadersMiddleware` | Security headers |
| `RateLimitMiddleware` | Token bucket |
| `TimeoutMiddleware` / `TimeoutHandler` | Handler deadline |
| `StripPrefixMiddleware` | Path prefix |
| `MethodOverrideMiddleware` | `_method` override |
| `NoCacheMiddleware` | Cache-Control |
| `HeadOrGetMiddleware` | HEAD/GET |
| `NormalizePathMiddleware` | Normalize path |
| `RequireContentTypeMiddleware` | Require CT |
| `RequireMethodsMiddleware` | Allow methods |
| `HTTPSRedirectMiddleware` | Redirect to HTTPS |
| `HealthCheck` / `ReadyCheck` | Probe handlers |

Option structs: `RequestIDOptions`, `CompressOptions`, `CORSOptions`, `SecureHeadersOptions`, `RateLimitOptions`.

## Application middleware

Auth sessions, CSRF, and similar concerns belong in your app (or framework), wrapping RawHTTP handlers — not inside RawHTTP’s package API.
