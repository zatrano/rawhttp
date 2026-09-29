package rawhttp

import (
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// ServeFile sends a local file as the response body.
//
// WARNING: do not pass user-controlled paths. Prefer FS with a fixed Root.
func ServeFile(ctx *Ctx, path string) {
	if path == "" {
		ctx.Error("empty path", 500)
		return
	}
	fi, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			ctx.NotFound()
			return
		}
		ctx.Error("stat error", 500)
		return
	}
	if fi.IsDir() {
		ctx.NotFound()
		return
	}
	mod := fi.ModTime().UTC().Truncate(time.Second)
	if !ctx.IfModifiedSince(mod) {
		_ = ctx.SetHeader("Last-Modified", mod.Format(http.TimeFormat))
		ctx.NotModified()
		return
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			ctx.NotFound()
			return
		}
		ctx.Error("open error", 500)
		return
	}
	ct := mime.TypeByExtension(filepath.Ext(path))
	if ct == "" {
		ct = "application/octet-stream"
	}
	ctx.SetContentType(ct)
	_ = ctx.SetHeader("Last-Modified", mod.Format(http.TimeFormat))
	// Caller must not close; stream is drained by writeResponse.
	// Wrap so we close after read completes via streamCloseReader.
	ctx.SetBodyStream(&fileBody{f: f}, int(fi.Size()))
}

// SendFile is a Ctx shortcut for ServeFile.
//
// WARNING: do not pass user-controlled paths. Prefer FS with a fixed Root.
func (c *Ctx) SendFile(path string) { ServeFile(c, path) }

type fileBody struct {
	f *os.File
}

func (fb *fileBody) Read(p []byte) (int, error) {
	n, err := fb.f.Read(p)
	if err != nil {
		_ = fb.f.Close()
		fb.f = nil
	}
	return n, err
}

func (fb *fileBody) Close() error {
	if fb.f == nil {
		return nil
	}
	err := fb.f.Close()
	fb.f = nil
	return err
}
