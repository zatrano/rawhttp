# Example: Middleware

```go
package main

import (
	"log"

	"github.com/zatrano/rawhttp"
)

func main() {
	h := rawhttp.Handler(func(ctx *rawhttp.Ctx) {
		ctx.SetBodyString("ok")
	})
	h = rawhttp.RecoverMiddleware(h, nil)
	h = rawhttp.RequestIDMiddleware(h, rawhttp.RequestIDOptions{})
	h = rawhttp.SecureHeadersMiddleware(h, rawhttp.SecureHeadersOptions{})

	log.Fatal(rawhttp.ListenAndServe(":8080", h))
}
```

Order: first-wrapped runs outermost. See [middleware.md](../middleware.md).
