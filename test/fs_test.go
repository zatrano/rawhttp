package test_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zatrano/rawhttp"
)

func TestFS_ServeFileAndTraversal(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "index.html"), []byte("<b>ok</b>"), 0o644); err != nil {
		t.Fatal(err)
	}

	h := rawhttp.NewFS(dir).Handler()
	srv := &rawhttp.Server{Handler: h}

	fc := newFakeConn([]byte("GET /hello.txt HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"))
	if err := srv.ServeConn(fc); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fc.w.String(), "hi") {
		t.Fatalf("body: %q", fc.w.String())
	}

	fc = newFakeConn([]byte("GET /sub/ HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"))
	if err := srv.ServeConn(fc); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fc.w.String(), "<b>ok</b>") {
		t.Fatalf("index: %q", fc.w.String())
	}

	fc = newFakeConn([]byte("GET /nope.txt HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"))
	if err := srv.ServeConn(fc); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fc.w.String(), "404") {
		t.Fatalf("want 404: %q", fc.w.String())
	}

	fc = newFakeConn([]byte("GET /..%2f..%2fetc%2fpasswd HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"))
	_ = srv.ServeConn(fc)
	if strings.Contains(fc.w.String(), "root:") {
		t.Fatalf("escaped root: %q", fc.w.String())
	}
}

func TestFS_IfModifiedSince(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	ims := fi.ModTime().UTC().Format(http.TimeFormat)

	h := rawhttp.NewFS(dir).Handler()
	srv := &rawhttp.Server{Handler: h, ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1}

	req := "GET /a.txt HTTP/1.1\r\nHost: localhost\r\nIf-Modified-Since: " + ims + "\r\nConnection: close\r\n\r\n"
	fc := newFakeConn([]byte(req))
	if err := srv.ServeConn(fc); err != nil {
		t.Fatal(err)
	}
	resp := fc.w.String()
	if !strings.Contains(resp, "304") {
		t.Fatalf("want 304: %q", resp)
	}
	if strings.Contains(resp, "data") {
		t.Fatalf("304 must not include body: %q", resp)
	}
}

func TestFS_ETagIfNoneMatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "b.txt")
	if err := os.WriteFile(path, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := rawhttp.NewFS(dir).Handler()
	srv := &rawhttp.Server{Handler: h, ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1}

	fc := newFakeConn([]byte("GET /b.txt HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"))
	if err := srv.ServeConn(fc); err != nil {
		t.Fatal(err)
	}
	resp := fc.w.String()
	etag := headerValue(resp, "ETag")
	if etag == "" {
		t.Fatalf("missing ETag: %q", resp)
	}

	req := "GET /b.txt HTTP/1.1\r\nHost: localhost\r\nIf-None-Match: " + etag + "\r\nConnection: close\r\n\r\n"
	fc = newFakeConn([]byte(req))
	if err := srv.ServeConn(fc); err != nil {
		t.Fatal(err)
	}
	resp = fc.w.String()
	if !strings.Contains(resp, "304") {
		t.Fatalf("want 304: %q", resp)
	}
	if strings.Contains(resp, "payload") {
		t.Fatalf("304 must not include body: %q", resp)
	}
}

func TestFS_CacheControl(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "c.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	fs := rawhttp.NewFS(dir)
	fs.CacheControl = "public, max-age=60"
	srv := &rawhttp.Server{Handler: fs.Handler(), ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1}
	fc := newFakeConn([]byte("GET /c.txt HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "Cache-Control: public, max-age=60") {
		t.Fatalf("want Cache-Control: %q", fc.w.String())
	}
}

func TestFS_DisableByteRanges(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "r.txt"), []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	fs := rawhttp.NewFS(dir)
	fs.DisableByteRanges = true
	srv := &rawhttp.Server{Handler: fs.Handler(), ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1}
	fc := newFakeConn([]byte("GET /r.txt HTTP/1.1\r\nHost: localhost\r\nRange: bytes=2-5\r\nConnection: close\r\n\r\n"))
	_ = srv.ServeConn(fc)
	resp := fc.w.String()
	if strings.Contains(resp, "206") || strings.Contains(resp, "Accept-Ranges:") {
		t.Fatalf("ranges disabled: %q", resp)
	}
	if !strings.Contains(resp, "0123456789") {
		t.Fatalf("want full body: %q", resp)
	}
}

func TestFS_Range(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "r.txt"), []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := rawhttp.NewFS(dir).Handler()
	srv := &rawhttp.Server{Handler: h, ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1}

	fc := newFakeConn([]byte("GET /r.txt HTTP/1.1\r\nHost: localhost\r\nRange: bytes=2-5\r\nConnection: close\r\n\r\n"))
	if err := srv.ServeConn(fc); err != nil {
		t.Fatal(err)
	}
	resp := fc.w.String()
	if !strings.Contains(resp, "206") {
		t.Fatalf("want 206: %q", resp)
	}
	if !strings.Contains(resp, "Content-Range: bytes 2-5/10") {
		t.Fatalf("want Content-Range: %q", resp)
	}
	if !strings.Contains(resp, "\r\n\r\n2345") {
		t.Fatalf("want body 2345: %q", resp)
	}

	fc = newFakeConn([]byte("GET /r.txt HTTP/1.1\r\nHost: localhost\r\nRange: bytes=99-100\r\nConnection: close\r\n\r\n"))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "416") {
		t.Fatalf("want 416: %q", fc.w.String())
	}
}

func TestFS_MethodNotAllowed(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := &rawhttp.Server{Handler: rawhttp.NewFS(dir).Handler(), ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1}
	fc := newFakeConn([]byte("POST /a.txt HTTP/1.1\r\nHost: localhost\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "405") {
		t.Fatalf("want 405: %q", fc.w.String())
	}
}

func TestFS_IndexEscape(t *testing.T) {
	dir := t.TempDir()
	evil := filepath.Join(dir, `a&b.txt`)
	if err := os.WriteFile(evil, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	fs := rawhttp.NewFS(dir)
	fs.GenerateIndexPages = true
	srv := &rawhttp.Server{Handler: fs.Handler(), ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1}
	fc := newFakeConn([]byte("GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"))
	_ = srv.ServeConn(fc)
	resp := fc.w.String()
	if strings.Contains(resp, `href="a&b.txt"`) {
		t.Fatalf("unescaped amp in index: %q", resp)
	}
	if !strings.Contains(resp, "a&amp;b.txt") {
		t.Fatalf("want escaped name: %q", resp)
	}
}

func TestFS_PrecompressedGzip(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "app.js")
	gz := plain + ".gz"
	if err := os.WriteFile(plain, []byte("console.log('hi')"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(gz, []byte("GZDATA"), 0o644); err != nil {
		t.Fatal(err)
	}
	fs := rawhttp.NewFS(dir)
	fs.Compress = true
	srv := &rawhttp.Server{Handler: fs.Handler(), ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1}

	fc := newFakeConn([]byte(
		"GET /app.js HTTP/1.1\r\nHost: localhost\r\nAccept-Encoding: gzip\r\nConnection: close\r\n\r\n",
	))
	_ = srv.ServeConn(fc)
	resp := fc.w.String()
	if !strings.Contains(resp, "Content-Encoding: gzip") {
		t.Fatalf("want gzip encoding: %q", resp)
	}
	if !strings.Contains(resp, "GZDATA") {
		t.Fatalf("want precompressed body: %q", resp)
	}
	if strings.Contains(resp, "Accept-Ranges:") {
		t.Fatalf("compressed entity should omit Accept-Ranges: %q", resp)
	}

	fc = newFakeConn([]byte(
		"GET /app.js HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n",
	))
	_ = srv.ServeConn(fc)
	resp = fc.w.String()
	if strings.Contains(resp, "Content-Encoding: gzip") {
		t.Fatalf("no Accept-Encoding → plain: %q", resp)
	}
	if !strings.Contains(resp, "console.log") {
		t.Fatalf("want plain body: %q", resp)
	}
}

func TestFS_PrecompressedBrotli(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "app.js")
	if err := os.WriteFile(plain, []byte("plain"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plain+".br", []byte("BRDATA"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plain+".gz", []byte("GZDATA"), 0o644); err != nil {
		t.Fatal(err)
	}
	fs := rawhttp.NewFS(dir)
	fs.Compress = true
	srv := &rawhttp.Server{Handler: fs.Handler(), ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1}

	fc := newFakeConn([]byte(
		"GET /app.js HTTP/1.1\r\nHost: localhost\r\nAccept-Encoding: gzip, br\r\nConnection: close\r\n\r\n",
	))
	_ = srv.ServeConn(fc)
	resp := fc.w.String()
	if !strings.Contains(resp, "Content-Encoding: br") || !strings.Contains(resp, "BRDATA") {
		t.Fatalf("prefer br: %q", resp)
	}
}

func TestFS_IfMatchPrecondition(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := rawhttp.NewFS(dir).Handler()
	srv := &rawhttp.Server{Handler: h, ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1}

	fc := newFakeConn([]byte(
		"GET /a.txt HTTP/1.1\r\nHost: localhost\r\nIf-Match: \"nope\"\r\nConnection: close\r\n\r\n",
	))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "412") {
		t.Fatalf("want 412: %q", fc.w.String())
	}
}

func headerValue(resp, name string) string {
	prefix := "\r\n" + name + ": "
	i := strings.Index(resp, prefix)
	if i < 0 {
		// try case as written
		prefix = "\r\n" + name + ": "
		i = strings.Index(resp, prefix)
		if i < 0 {
			return ""
		}
	}
	rest := resp[i+len(prefix):]
	j := strings.Index(rest, "\r\n")
	if j < 0 {
		return strings.TrimSpace(rest)
	}
	return strings.TrimSpace(rest[:j])
}

func TestFS_HideDotFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ok.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".secret"), []byte("nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	fs := rawhttp.NewFS(dir)
	fs.HideDotFiles = true
	fs.GenerateIndexPages = true
	srv := &rawhttp.Server{Handler: fs.Handler(), ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1}

	fc := newFakeConn([]byte("GET /.secret HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "404") {
		t.Fatalf("want 404 for dotfile: %q", fc.w.String())
	}

	fc = newFakeConn([]byte("GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"))
	_ = srv.ServeConn(fc)
	resp := fc.w.String()
	if strings.Contains(resp, ".secret") {
		t.Fatalf("index leaked dotfile: %q", resp)
	}
	if !strings.Contains(resp, "ok.txt") {
		t.Fatalf("index missing ok.txt: %q", resp)
	}
}

func TestFS_RejectSymlinks(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real.txt")
	if err := os.WriteFile(real, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	fs := rawhttp.NewFS(dir)
	fs.RejectSymlinks = true
	srv := &rawhttp.Server{Handler: fs.Handler(), ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1}

	fc := newFakeConn([]byte("GET /link.txt HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "403") {
		t.Fatalf("want 403 for symlink: %q", fc.w.String())
	}

	fc = newFakeConn([]byte("GET /real.txt HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "data") {
		t.Fatalf("want real file: %q", fc.w.String())
	}
}
