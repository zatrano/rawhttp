package rawhttp

import (
	"bytes"
	"html"
	"io"
	iofs "io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// FS serves static files from Root and/or an io/fs.FS with path-traversal hardening.
type FS struct {
	// Root is the filesystem directory to serve when FS is nil.
	// When FS is set, Root is an optional subdirectory prefix inside that FS.
	Root string

	// FS, when set, serves files from this filesystem (embed.FS, os.DirFS, …).
	// Prefer this for embedded assets. RejectSymlinks is ignored when FS is set.
	FS iofs.FS

	// IndexNames are tried when the request path is a directory.
	// Default: []string{"index.html"}
	IndexNames []string

	// PathRewrite rewrites the request path before joining Root.
	// If nil, ctx.Path is used (leading '/' stripped).
	PathRewrite func(ctx *Ctx) string

	// NotFound is called when a file is missing. Nil → 404 text.
	NotFound Handler

	// GenerateIndexPages lists directory contents when no index file exists.
	GenerateIndexPages bool

	// CacheControl, when non-empty, is set on successful file responses
	// (including 304). Example: "public, max-age=3600".
	CacheControl string

	// DisableByteRanges omits Accept-Ranges and ignores Range / If-Range.
	DisableByteRanges bool

	// Compress serves a sibling precompressed file when the client Accept-Encoding
	// allows it and path+".br"/path+".gz" exists and is not older than the original.
	// Prefers brotli over gzip. Sets Content-Encoding. Range requests use the
	// uncompressed file.
	Compress bool

	// RejectSymlinks refuses to serve a path that is itself a symlink or whose
	// resolved real path escapes Root (symlink escape). Default false.
	// Ignored when FS is set (virtual filesystems have no OS symlinks).
	RejectSymlinks bool

	// HideDotFiles returns 404 for any path component that starts with '.'.
	// Default false.
	HideDotFiles bool
}

// NewFS returns an FS rooted at root.
func NewFS(root string) *FS {
	return &FS{Root: root}
}

// Handler returns a Handler that serves files.
func (fs *FS) Handler() Handler {
	return fs.Serve
}

// Serve handles one static-file request.
func (fs *FS) Serve(ctx *Ctx) {
	if !isGetMethod(ctx.Method) && !ctx.head {
		_ = ctx.MethodNotAllowed("GET, HEAD")
		return
	}
	if fs.FS != nil {
		fs.serveIOFS(ctx)
		return
	}
	root := fs.Root
	if root == "" {
		ctx.Error("fs root not configured", 500)
		return
	}

	rel := fs.requestRel(ctx)
	if rel == ".." || strings.HasPrefix(rel, "../") {
		ctx.Forbidden()
		return
	}
	if strings.Contains(rel, "..") || strings.Contains(rel, "\\") || strings.ContainsAny(rel, "\x00") {
		ctx.Forbidden()
		return
	}

	full, err := filepath.Abs(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		ctx.Forbidden()
		return
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		ctx.Error("fs root invalid", 500)
		return
	}
	sep := string(os.PathSeparator)
	if full != rootAbs && !strings.HasPrefix(full, rootAbs+sep) {
		ctx.Forbidden()
		return
	}

	if fs.HideDotFiles && pathHasDotComponent(rel) {
		fs.notFound(ctx)
		return
	}

	if fs.RejectSymlinks {
		if denied, missing := fs.symlinkDenied(full, rootAbs); denied {
			if missing {
				fs.notFound(ctx)
			} else {
				ctx.Forbidden()
			}
			return
		}
	}

	fi, err := os.Stat(full)
	if err != nil {
		if os.IsNotExist(err) {
			fs.notFound(ctx)
			return
		}
		ctx.Error("stat error", 500)
		return
	}

	if fi.IsDir() {
		names := fs.IndexNames
		if len(names) == 0 {
			names = []string{"index.html"}
		}
		for _, name := range names {
			if fs.HideDotFiles && len(name) > 0 && name[0] == '.' {
				continue
			}
			idx := filepath.Join(full, name)
			if fs.RejectSymlinks {
				if denied, _ := fs.symlinkDenied(idx, rootAbs); denied {
					continue
				}
			}
			ifi, err := os.Stat(idx)
			if err == nil && !ifi.IsDir() {
				fs.serveOSFile(ctx, idx, ifi)
				return
			}
		}
		if fs.GenerateIndexPages {
			fs.serveIndexOS(ctx, full, rel)
			return
		}
		fs.notFound(ctx)
		return
	}

	fs.serveOSFile(ctx, full, fi)
}

func (fs *FS) requestRel(ctx *Ctx) string {
	rel := ""
	if fs.PathRewrite != nil {
		rel = fs.PathRewrite(ctx)
	} else {
		rel = string(ctx.Path)
	}
	rel = strings.TrimPrefix(rel, "/")
	clean := path.Clean("/" + rel)
	if clean == "/" {
		return ""
	}
	return clean[1:]
}

func (fs *FS) serveIOFS(ctx *Ctx) {
	rel := fs.requestRel(ctx)
	if strings.Contains(rel, "..") || strings.Contains(rel, "\\") || strings.ContainsAny(rel, "\x00") {
		ctx.Forbidden()
		return
	}
	if fs.HideDotFiles && pathHasDotComponent(rel) {
		fs.notFound(ctx)
		return
	}

	name := rel
	if fs.Root != "" {
		root := strings.Trim(strings.ReplaceAll(fs.Root, "\\", "/"), "/")
		if root != "" {
			if name == "" {
				name = root
			} else {
				name = root + "/" + name
			}
		}
	}
	if name == "" {
		name = "."
	}

	fi, err := fs.statIOFS(name)
	if err != nil {
		if os.IsNotExist(err) {
			fs.notFound(ctx)
			return
		}
		ctx.Error("stat error", 500)
		return
	}

	if fi.IsDir() {
		names := fs.IndexNames
		if len(names) == 0 {
			names = []string{"index.html"}
		}
		base := name
		if base == "." {
			base = ""
		}
		for _, iname := range names {
			if fs.HideDotFiles && len(iname) > 0 && iname[0] == '.' {
				continue
			}
			idx := iname
			if base != "" {
				idx = base + "/" + iname
			}
			ifi, err := fs.statIOFS(idx)
			if err == nil && !ifi.IsDir() {
				fs.serveIOFSFile(ctx, idx, ifi)
				return
			}
		}
		if fs.GenerateIndexPages {
			fs.serveIndexIOFS(ctx, name, rel)
			return
		}
		fs.notFound(ctx)
		return
	}

	fs.serveIOFSFile(ctx, name, fi)
}

func (fs *FS) statIOFS(name string) (iofs.FileInfo, error) {
	f, err := fs.FS.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return f.Stat()
}

func (fs *FS) serveIOFSFile(ctx *Ctx, name string, fi iofs.FileInfo) {
	mod := fi.ModTime().UTC().Truncate(time.Second)
	size := fi.Size()
	etag := fsWeakETag(mod, size)
	lm := mod.Format(http.TimeFormat)

	_ = ctx.SetHeader("Last-Modified", lm)
	_ = ctx.SetHeader("ETag", etag)
	if !fs.DisableByteRanges {
		_ = ctx.SetHeader("Accept-Ranges", "bytes")
	}
	if fs.CacheControl != "" {
		_ = ctx.SetHeader("Cache-Control", fs.CacheControl)
	}

	if im := ctx.Header("If-Match"); len(im) > 0 {
		if !etagMatch(im, etag) {
			ctx.SetStatusCode(412)
			return
		}
	}
	if ius := ctx.Header("If-Unmodified-Since"); len(ius) > 0 {
		if t, err := http.ParseTime(string(ius)); err == nil && mod.After(t) {
			ctx.SetStatusCode(412)
			return
		}
	}
	if inm := ctx.Header("If-None-Match"); len(inm) > 0 {
		if etagMatch(inm, etag) {
			ctx.SetStatusCode(304)
			return
		}
	} else if ims := ctx.Header("If-Modified-Since"); len(ims) > 0 {
		if t, err := http.ParseTime(string(ims)); err == nil && !mod.After(t) {
			ctx.SetStatusCode(304)
			return
		}
	}

	ct := mime.TypeByExtension(path.Ext(name))
	if ct == "" {
		ct = "application/octet-stream"
	}
	ctx.SetContentType(ct)

	start, end, ok, partial := int64(0), size-1, true, false
	if !fs.DisableByteRanges {
		start, end, ok, partial = parseByteRange(ctx.Header("Range"), size)
		if ir := ctx.Header("If-Range"); len(ir) > 0 && partial {
			if !etagMatch(ir, etag) && !ifRangeMatchesLM(ir, mod) {
				partial = false
				ok = true
				start, end = 0, size-1
			}
		}
	}
	if !ok {
		ctx.SetStatusCode(416)
		_ = ctx.SetHeader("Content-Range", "bytes */"+strconv.FormatInt(size, 10))
		return
	}

	serveName := name
	serveSize := size
	if fs.Compress && !partial {
		ae := ctx.Header("Accept-Encoding")
		for _, cand := range precompressedCandidates(ae) {
			pc := name + cand.ext
			if pci, err := fs.statIOFS(pc); err == nil && !pci.IsDir() && !pci.ModTime().Before(fi.ModTime()) {
				serveName = pc
				serveSize = pci.Size()
				_ = ctx.SetHeader("Content-Encoding", cand.enc)
				_ = ctx.SetHeader("Vary", "Accept-Encoding")
				ctx.DelHeader("Accept-Ranges")
				partial = false
				break
			}
		}
	}

	f, err := fs.FS.Open(serveName)
	if err != nil {
		fs.notFound(ctx)
		return
	}
	rc, okSeek := f.(io.ReadSeeker)
	if partial {
		if !okSeek {
			_ = f.Close()
			ctx.Error("range not supported", 500)
			return
		}
		if _, err := rc.Seek(start, io.SeekStart); err != nil {
			_ = f.Close()
			ctx.Error("seek error", 500)
			return
		}
		ctx.SetStatusCode(206)
		_ = ctx.SetHeader("Content-Range",
			"bytes "+strconv.FormatInt(start, 10)+"-"+strconv.FormatInt(end, 10)+"/"+strconv.FormatInt(size, 10))
		ctx.SetBodyStream(f, int(end-start+1))
		return
	}
	ctx.SetBodyStream(f, int(serveSize))
}

func (fs *FS) serveIndexIOFS(ctx *Ctx, dir, rel string) {
	entries, err := iofs.ReadDir(fs.FS, dir)
	if err != nil {
		ctx.Error("readdir error", 500)
		return
	}
	var b strings.Builder
	b.WriteString("<html><body><h1>Index of /")
	b.WriteString(html.EscapeString(rel))
	b.WriteString("</h1><ul>")
	if rel != "" {
		b.WriteString(`<li><a href="../">../</a></li>`)
	}
	for _, e := range entries {
		name := e.Name()
		if fs.HideDotFiles && len(name) > 0 && name[0] == '.' {
			continue
		}
		href := name
		display := name
		if e.IsDir() {
			href += "/"
			display += "/"
		}
		b.WriteString(`<li><a href="`)
		b.WriteString(html.EscapeString(href))
		b.WriteString(`">`)
		b.WriteString(html.EscapeString(display))
		b.WriteString("</a></li>")
	}
	b.WriteString("</ul></body></html>")
	ctx.SetContentType("text/html; charset=utf-8")
	ctx.SetBodyString(b.String())
}

func (fs *FS) notFound(ctx *Ctx) {
	if fs.NotFound != nil {
		fs.NotFound(ctx)
		return
	}
	ctx.NotFound()
}

func pathHasDotComponent(rel string) bool {
	if rel == "" {
		return false
	}
	for _, p := range strings.Split(rel, "/") {
		if p != "" && p[0] == '.' {
			return true
		}
	}
	return false
}

// symlinkDenied reports whether full must not be served under RejectSymlinks.
// missing is true when the path does not exist (caller may 404).
func (fs *FS) symlinkDenied(full, rootAbs string) (denied, missing bool) {
	fi, err := os.Lstat(full)
	if err != nil {
		if os.IsNotExist(err) {
			return true, true
		}
		return true, false
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return true, false
	}
	real, err := filepath.EvalSymlinks(full)
	if err != nil {
		if os.IsNotExist(err) {
			return true, true
		}
		return true, false
	}
	realAbs, err := filepath.Abs(real)
	if err != nil {
		return true, false
	}
	sep := string(os.PathSeparator)
	if realAbs != rootAbs && !strings.HasPrefix(realAbs, rootAbs+sep) {
		return true, false
	}
	return false, false
}

func (fs *FS) serveOSFile(ctx *Ctx, path string, fi os.FileInfo) {
	mod := fi.ModTime().UTC().Truncate(time.Second)
	size := fi.Size()
	etag := fsWeakETag(mod, size)
	lm := mod.Format(http.TimeFormat)

	_ = ctx.SetHeader("Last-Modified", lm)
	_ = ctx.SetHeader("ETag", etag)
	if !fs.DisableByteRanges {
		_ = ctx.SetHeader("Accept-Ranges", "bytes")
	}
	if fs.CacheControl != "" {
		_ = ctx.SetHeader("Cache-Control", fs.CacheControl)
	}

	if im := ctx.Header("If-Match"); len(im) > 0 {
		if !etagMatch(im, etag) {
			ctx.SetStatusCode(412)
			return
		}
	}
	if ius := ctx.Header("If-Unmodified-Since"); len(ius) > 0 {
		if t, err := http.ParseTime(string(ius)); err == nil && mod.After(t) {
			ctx.SetStatusCode(412)
			return
		}
	}

	if inm := ctx.Header("If-None-Match"); len(inm) > 0 {
		if etagMatch(inm, etag) {
			ctx.SetStatusCode(304)
			return
		}
	} else if ims := ctx.Header("If-Modified-Since"); len(ims) > 0 {
		if t, err := http.ParseTime(string(ims)); err == nil && !mod.After(t) {
			ctx.SetStatusCode(304)
			return
		}
	}

	ct := mime.TypeByExtension(filepath.Ext(path))
	if ct == "" {
		ct = "application/octet-stream"
	}
	ctx.SetContentType(ct)

	start, end, ok, partial := int64(0), size-1, true, false
	if !fs.DisableByteRanges {
		start, end, ok, partial = parseByteRange(ctx.Header("Range"), size)
		if ir := ctx.Header("If-Range"); len(ir) > 0 && partial {
			if !etagMatch(ir, etag) && !ifRangeMatchesLM(ir, mod) {
				partial = false
				ok = true
				start, end = 0, size-1
			}
		}
	}
	if !ok {
		ctx.SetStatusCode(416)
		_ = ctx.SetHeader("Content-Range", "bytes */"+strconv.FormatInt(size, 10))
		return
	}

	// Precompressed siblings: only for full-entity responses when Compress is set.
	servePath := path
	serveSize := size
	if fs.Compress && !partial {
		ae := ctx.Header("Accept-Encoding")
		for _, cand := range precompressedCandidates(ae) {
			pcPath := path + cand.ext
			if pci, err := os.Stat(pcPath); err == nil && !pci.IsDir() && !pci.ModTime().Before(fi.ModTime()) {
				servePath = pcPath
				serveSize = pci.Size()
				_ = ctx.SetHeader("Content-Encoding", cand.enc)
				_ = ctx.SetHeader("Vary", "Accept-Encoding")
				ctx.DelHeader("Accept-Ranges")
				break
			}
		}
	}

	f, err := os.Open(servePath)
	if err != nil {
		fs.notFound(ctx)
		return
	}

	if partial {
		if _, err := f.Seek(start, io.SeekStart); err != nil {
			_ = f.Close()
			ctx.Error("seek error", 500)
			return
		}
		ctx.SetStatusCode(206)
		_ = ctx.SetHeader("Content-Range",
			"bytes "+strconv.FormatInt(start, 10)+"-"+strconv.FormatInt(end, 10)+"/"+strconv.FormatInt(size, 10))
		ctx.SetBodyStream(f, int(end-start+1))
		return
	}

	// Ownership of f transfers to the response stream; writeResponse closes it.
	ctx.SetBodyStream(f, int(serveSize))
}

type precompCand struct {
	enc, ext string
}

func precompressedCandidates(ae []byte) []precompCand {
	var out []precompCand
	if acceptEncodingHas(ae, "br") {
		out = append(out, precompCand{"br", ".br"})
	}
	if acceptEncodingHas(ae, "gzip") || acceptEncodingHas(ae, "x-gzip") {
		out = append(out, precompCand{"gzip", ".gz"})
	}
	return out
}

func acceptEncodingHas(h []byte, coding string) bool {
	if len(h) == 0 || coding == "" {
		return false
	}
	off := 0
	for off <= len(h) {
		end := off
		for end < len(h) && h[end] != ',' {
			end++
		}
		part := h[off:end]
		for len(part) > 0 && (part[0] == ' ' || part[0] == '\t') {
			part = part[1:]
		}
		for len(part) > 0 && (part[len(part)-1] == ' ' || part[len(part)-1] == '\t') {
			part = part[:len(part)-1]
		}
		if i := bytes.IndexByte(part, ';'); i >= 0 {
			part = part[:i]
			for len(part) > 0 && (part[len(part)-1] == ' ' || part[len(part)-1] == '\t') {
				part = part[:len(part)-1]
			}
		}
		if equalFoldStr(part, coding) {
			return true
		}
		if end >= len(h) {
			break
		}
		off = end + 1
	}
	return false
}

func fsWeakETag(mod time.Time, size int64) string {
	return `W/"` + strconv.FormatInt(mod.Unix(), 16) + "-" + strconv.FormatInt(size, 16) + `"`
}

func etagMatch(header []byte, etag string) bool {
	for len(header) > 0 && (header[0] == ' ' || header[0] == '\t') {
		header = header[1:]
	}
	for len(header) > 0 && (header[len(header)-1] == ' ' || header[len(header)-1] == '\t') {
		header = header[:len(header)-1]
	}
	if len(header) == 1 && header[0] == '*' {
		return true
	}
	want := stripWeakETagBytes([]byte(etag))
	off := 0
	for off <= len(header) {
		end := off
		for end < len(header) && header[end] != ',' {
			end++
		}
		part := header[off:end]
		for len(part) > 0 && (part[0] == ' ' || part[0] == '\t') {
			part = part[1:]
		}
		for len(part) > 0 && (part[len(part)-1] == ' ' || part[len(part)-1] == '\t') {
			part = part[:len(part)-1]
		}
		if (len(part) == 1 && part[0] == '*') || bytes.Equal(stripWeakETagBytes(part), want) {
			return true
		}
		if end >= len(header) {
			break
		}
		off = end + 1
	}
	return false
}

func stripWeakETagBytes(v []byte) []byte {
	for len(v) > 0 && (v[0] == ' ' || v[0] == '\t') {
		v = v[1:]
	}
	if len(v) >= 2 && (v[0] == 'W' || v[0] == 'w') && v[1] == '/' {
		v = v[2:]
	}
	return v
}

func ifRangeMatchesLM(header []byte, mod time.Time) bool {
	t, err := http.ParseTime(string(header))
	if err != nil {
		return false
	}
	return !mod.After(t) && !t.After(mod)
}

// parseByteRange parses a single bytes= range. Empty header → full entity.
// Unsatisfiable / malformed → ok=false (caller returns 416).
// Multi-range requests are ignored (full entity).
func parseByteRange(h []byte, size int64) (start, end int64, ok, partial bool) {
	if size <= 0 {
		if len(h) == 0 {
			return 0, -1, true, false
		}
		return 0, 0, false, false
	}
	if len(h) == 0 {
		return 0, size - 1, true, false
	}
	const prefix = "bytes="
	if len(h) < len(prefix) || !equalFoldStr(h[:len(prefix)], prefix) {
		return 0, 0, false, false
	}
	spec := string(h[len(prefix):])
	if strings.Contains(spec, ",") {
		// Multi-range: serve full body rather than 416.
		return 0, size - 1, true, false
	}
	dash := strings.IndexByte(spec, '-')
	if dash < 0 {
		return 0, 0, false, false
	}
	left, right := spec[:dash], spec[dash+1:]
	switch {
	case left == "" && right != "":
		// suffix: last N bytes
		n, err := strconv.ParseInt(right, 10, 64)
		if err != nil || n <= 0 {
			return 0, 0, false, false
		}
		if n > size {
			n = size
		}
		return size - n, size - 1, true, true
	case left != "":
		s, err := strconv.ParseInt(left, 10, 64)
		if err != nil || s < 0 {
			return 0, 0, false, false
		}
		if s >= size {
			return 0, 0, false, false
		}
		e := size - 1
		if right != "" {
			e, err = strconv.ParseInt(right, 10, 64)
			if err != nil || e < s {
				return 0, 0, false, false
			}
			if e >= size {
				e = size - 1
			}
		}
		return s, e, true, true
	default:
		return 0, 0, false, false
	}
}

func (fs *FS) serveIndexOS(ctx *Ctx, dir, rel string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		ctx.Error("readdir error", 500)
		return
	}
	var b strings.Builder
	b.WriteString("<html><body><h1>Index of /")
	b.WriteString(html.EscapeString(rel))
	b.WriteString("</h1><ul>")
	if rel != "" {
		b.WriteString(`<li><a href="../">../</a></li>`)
	}
	for _, e := range entries {
		name := e.Name()
		if fs.HideDotFiles && len(name) > 0 && name[0] == '.' {
			continue
		}
		href := name
		display := name
		if e.IsDir() {
			href += "/"
			display += "/"
		}
		b.WriteString(`<li><a href="`)
		b.WriteString(html.EscapeString(href))
		b.WriteString(`">`)
		b.WriteString(html.EscapeString(display))
		b.WriteString("</a></li>")
	}
	b.WriteString("</ul></body></html>")
	ctx.SetContentType("text/html; charset=utf-8")
	ctx.SetBodyString(b.String())
}
