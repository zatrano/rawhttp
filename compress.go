package rawhttp

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"sync"
)

var (
	gzipPool = sync.Pool{New: func() any {
		w, _ := gzip.NewWriterLevel(nil, gzip.DefaultCompression)
		return w
	}}
	flatePool = sync.Pool{New: func() any {
		w, _ := flate.NewWriter(nil, flate.DefaultCompression)
		return w
	}}
	compressBufPool = sync.Pool{New: func() any {
		return new(bytes.Buffer)
	}}
)

// CompressOptions controls response compression behavior.
type CompressOptions struct {
	// MinLength skips compression for smaller bodies. Default 256.
	MinLength int
	// ContentTypes, when non-empty, only compresses responses whose
	// Content-Type matches one entry (prefix match, e.g. "text/" or
	// "application/json"). Empty → compress any type.
	ContentTypes []string
}

// CompressMiddleware wraps h and compresses eligible in-memory responses.
// Streaming bodies (SetBodyStream) are left untouched.
func CompressMiddleware(h Handler, opt CompressOptions) Handler {
	if opt.MinLength <= 0 {
		opt.MinLength = 256
	}
	return func(ctx *Ctx) {
		h(ctx)
		if ctx.Hijacked() {
			return
		}
		compressResponse(ctx, opt)
	}
}

func compressResponse(ctx *Ctx, opt CompressOptions) {
	ae := ctx.Header("Accept-Encoding")
	if len(ae) == 0 {
		return
	}
	rb := ctx.ResponseBody()
	if len(rb) < opt.MinLength {
		return
	}
	if len(ctx.Header("Content-Encoding")) > 0 {
		return
	}
	if len(opt.ContentTypes) > 0 && !contentTypeAllowed(ctx.contentType, opt.ContentTypes) {
		return
	}

	var enc string
	switch {
	case acceptsEncodingToken(ae, gzipTok):
		enc = "gzip"
	case acceptsEncodingToken(ae, deflateTok):
		enc = "deflate"
	default:
		return
	}

	buf := compressBufPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer compressBufPool.Put(buf)

	switch enc {
	case "gzip":
		w := gzipPool.Get().(*gzip.Writer)
		w.Reset(buf)
		_, _ = w.Write(rb)
		_ = w.Close()
		gzipPool.Put(w)
	case "deflate":
		w := flatePool.Get().(*flate.Writer)
		w.Reset(buf)
		_, _ = w.Write(rb)
		_ = w.Close()
		flatePool.Put(w)
	}
	if buf.Len() == 0 || buf.Len() >= len(rb) {
		return
	}
	_ = ctx.SetHeader("Content-Encoding", enc)
	_ = ctx.SetHeader("Vary", "Accept-Encoding")
	ctx.SetBody(append([]byte(nil), buf.Bytes()...))
}

var (
	gzipTok    = []byte("gzip")
	deflateTok = []byte("deflate")
	starTok    = []byte("*")
)

func acceptsEncodingToken(header, coding []byte) bool {
	i := 0
	n := len(header)
	for i < n {
		for i < n && (header[i] == ' ' || header[i] == '\t' || header[i] == ',') {
			i++
		}
		if i >= n {
			break
		}
		start := i
		for i < n && header[i] != ',' && header[i] != ';' && header[i] != ' ' && header[i] != '\t' {
			i++
		}
		tok := header[start:i]
		if equalFoldBytes(tok, coding) || equalFoldBytes(tok, starTok) {
			return true
		}
		for i < n && header[i] != ',' {
			i++
		}
	}
	return false
}

func contentTypeAllowed(ct []byte, allow []string) bool {
	// Strip parameters after ';'
	media := ct
	if i := indexByte(ct, ';'); i >= 0 {
		media = ct[:i]
	}
	for len(media) > 0 && (media[len(media)-1] == ' ' || media[len(media)-1] == '\t') {
		media = media[:len(media)-1]
	}
	for _, a := range allow {
		if a == "" {
			continue
		}
		ab := []byte(a)
		if len(media) >= len(ab) && equalFoldBytes(media[:len(ab)], ab) {
			return true
		}
	}
	return false
}

func indexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}
