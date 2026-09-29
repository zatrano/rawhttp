package test_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/zatrano/rawhttp"
)

func TestFS_EmbedStyleMapFS(t *testing.T) {
	mem := fstest.MapFS{
		"hello.txt":      &fstest.MapFile{Data: []byte("embedded-hi")},
		"sub/index.html": &fstest.MapFile{Data: []byte("<b>idx</b>")},
		".secret":        &fstest.MapFile{Data: []byte("nope")},
	}
	h := &rawhttp.FS{FS: mem, HideDotFiles: true}
	srv := &rawhttp.Server{Handler: h.Handler(), ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1}

	fc := newFakeConn([]byte("GET /hello.txt HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "embedded-hi") {
		t.Fatalf("want embed body: %q", fc.w.String())
	}

	fc = newFakeConn([]byte("GET /sub/ HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "<b>idx</b>") {
		t.Fatalf("want index: %q", fc.w.String())
	}

	fc = newFakeConn([]byte("GET /.secret HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "404") {
		t.Fatalf("want 404 for dotfile: %q", fc.w.String())
	}
}

func TestFS_DirFSWithRootPrefix(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "assets")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "a.txt"), []byte("via-dirfs"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := &rawhttp.FS{FS: os.DirFS(dir), Root: "assets"}
	srv := &rawhttp.Server{Handler: h.Handler(), ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1}
	fc := newFakeConn([]byte("GET /a.txt HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "via-dirfs") {
		t.Fatalf("want dirfs body: %q", fc.w.String())
	}
}
