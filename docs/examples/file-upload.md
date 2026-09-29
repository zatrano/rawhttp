# Example: File upload

```go
package main

import (
	"log"
	"os"
	"path/filepath"

	"github.com/zatrano/rawhttp"
)

func main() {
	log.Fatal(rawhttp.ListenAndServe(":8080", func(ctx *rawhttp.Ctx) {
		if !ctx.IsPost() || !ctx.PathEqual("/upload") {
			ctx.NotFound()
			return
		}
		fh, err := ctx.FormFile("file")
		if err != nil {
			ctx.BadRequest()
			return
		}
		dst := filepath.Join(os.TempDir(), filepath.Base(fh.Filename))
		if err := rawhttp.SaveMultipartFile(fh, dst, 16<<20); err != nil {
			ctx.Error("save failed", 500)
			return
		}
		ctx.SetBodyString("saved")
	}))
}
```

Multipart limits: `Server.MaxMultipartMemory`, `MaxMultipartFiles`, `MaxMultipartParts`.
