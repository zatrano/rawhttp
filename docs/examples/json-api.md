# Example: JSON API

```go
package main

import (
	"log"

	"github.com/zatrano/rawhttp"
)

type echoIn struct {
	Msg string `json:"msg"`
}

func main() {
	log.Fatal(rawhttp.ListenAndServe(":8080", func(ctx *rawhttp.Ctx) {
		if !ctx.IsPost() || !ctx.PathEqual("/api/echo") {
			ctx.NotFound()
			return
		}
		var in echoIn
		if err := ctx.ReadJSON(&in); err != nil {
			ctx.BadRequest()
			return
		}
		if err := ctx.WriteJSON(in); err != nil {
			ctx.Error("encode failed", 500)
		}
	}))
}
```

```bash
curl -s -X POST http://127.0.0.1:8080/api/echo \
  -H 'Content-Type: application/json' \
  -d '{"msg":"hello"}'
```
