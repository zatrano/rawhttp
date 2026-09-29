# Routing

RawHTTP has **no** built-in path-parameter router. There is no `/users/:id` or `/users/{id}` syntax in this package.

Routing is manual: branch on `ctx.Method` / `ctx.Path` (and helpers) inside your `Handler`, or put a router in your own framework on top.

## Pattern

```go
func router(ctx *rawhttp.Ctx) {
	switch {
	case ctx.IsGet() && ctx.PathEqual("/"):
		home(ctx)
	case ctx.IsGet() && ctx.PathHasPrefix("/assets/"):
		static(ctx)
	case ctx.IsPost() && ctx.PathEqual("/api/items"):
		createItem(ctx)
	case ctx.IsGet() && ctx.PathHasPrefix("/users/"):
		// parse id yourself from ctx.Path — RawHTTP will not expand :id / {id}
		userByPath(ctx)
	default:
		ctx.NotFound()
	}
}
```

## Middleware-assisted

- `StripPrefixMiddleware` — strip a path prefix before the next handler
- `RequireMethodsMiddleware` — allow-list methods
- `HeadOrGetMiddleware` — treat HEAD like GET without body
- `NormalizePathMiddleware` — path cleanup helper

## Frameworks on top

A framework on top of RawHTTP should own rich routing. RawHTTP supplies `Path`, `Method`, and the handler hook.

```text
Your router
      │
      ▼
RawHTTP Handler / Ctx
```
