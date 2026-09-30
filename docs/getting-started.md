# Getting started

Install RawHTTP and run a minimal server in a few minutes.

## Installation

```bash
go get github.com/zatrano/rawhttp@v0.2.0
```

Module path: `github.com/zatrano/rawhttp`. Go 1.22+.

Tests live in a separate module (`test/`): `cd test && go test ./...`.
## Minimal server

```go
package main

import (
	"log"

	"github.com/zatrano/rawhttp"
)

func main() {
	log.Fatal(rawhttp.ListenAndServe(":8080", func(ctx *rawhttp.Ctx) {
		ctx.SetBody([]byte("Hello, World!"))
	}))
}
```

`ListenAndServe` creates a `Server` with the given `Handler` and listens on TCP.

## Route (manual)

RawHTTP has **no** built-in path-parameter router. Branch on `ctx.Path` / method helpers:

```go
func main() {
	log.Fatal(rawhttp.ListenAndServe(":8080", func(ctx *rawhttp.Ctx) {
		switch {
		case ctx.IsGet() && ctx.PathEqual("/"):
			ctx.SetBody([]byte("home"))
		case ctx.IsGet() && ctx.PathEqual("/health"):
			ctx.SetBody([]byte("ok"))
		default:
			ctx.NotFound()
		}
	}))
}
```

Helpers: `PathEqual`, `PathHasPrefix`, `IsGet`, `IsPost`, `MethodEqual`, …

## Request

```go
func handler(ctx *rawhttp.Ctx) {
	method := string(ctx.Method)
	path := string(ctx.Path)
	q := ctx.QueryArgs().GetString("page")
	ua := string(ctx.UserAgent())
	_ = method
	_ = path
	_ = q
	_ = ua
}
```

Slices into the connection buffer (`Method`, `Path`, `Header` results) are valid only until the handler returns.

## Response

```go
ctx.SetStatusCode(201)
ctx.SetContentType("text/plain; charset=utf-8")
_ = ctx.SetHeader("X-App", "demo")
ctx.SetBodyString("created")
```

Shortcuts: `NotFound()`, `BadRequest()`, `Redirect("/elsewhere", 302)`, `Success()`, `SuccessString("ok")`.

## JSON response

```go
type Msg struct {
	Hello string `json:"hello"`
}

func handler(ctx *rawhttp.Ctx) {
	if err := ctx.WriteJSON(Msg{Hello: "world"}); err != nil {
		ctx.Error("encode failed", 500)
	}
}
```

Read JSON body: `ctx.ReadJSON(&v)`.

## Middleware

Middleware is **handler wrapping** (no global stack type):

```go
func main() {
	h := rawhttp.Handler(func(ctx *rawhttp.Ctx) {
		ctx.SetBodyString("ok")
	})
	h = rawhttp.RecoverMiddleware(h, nil) // onPanic may be nil
	h = rawhttp.RequestIDMiddleware(h, rawhttp.RequestIDOptions{})
	log.Fatal(rawhttp.ListenAndServe(":8080", h))
}
```

Outer middleware runs first on the way in.

## Next

- [Concepts](concepts.md)
- [Server options](server.md)
- [Examples](examples/hello-world.md)
