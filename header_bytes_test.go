package rawhttp

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestSetHeaderBytesMatchesSetHeader(t *testing.T) {
	headers := []struct{ key, val string }{
		{"X-Request-Id", "abc-123"},
		{"x-frame-options", "SAMEORIGIN"},
		{"X-Content-Type-Options", "nosniff"},
	}
	req := "GET /plaintext HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
	fromString := serveHeaders(t, req, func(ctx *Ctx) {
		for _, h := range headers {
			if err := ctx.SetHeader(h.key, h.val); err != nil {
				t.Fatal(err)
			}
		}
		ctx.SetBodyString("Hello, World!")
	})
	fromBytes := serveHeaders(t, req, func(ctx *Ctx) {
		for _, h := range headers {
			if err := ctx.SetHeaderBytes(h.key, []byte(h.val)); err != nil {
				t.Fatal(err)
			}
		}
		ctx.SetBodyString("Hello, World!")
	})
	if !bytes.Equal(fromString, fromBytes) {
		t.Fatalf("response bytes differ\nstring: %q\nbytes:  %q", fromString, fromBytes)
	}
	// Order, case, and values are the bytes SetHeader wrote. SetHeaderBytes matched them.
	wire := string(fromString)
	want := "X-Request-Id: abc-123\r\nx-frame-options: SAMEORIGIN\r\nX-Content-Type-Options: nosniff\r\n"
	if !strings.Contains(wire, want) {
		t.Fatalf("header block missing\nwant %q\nwire %q", want, wire)
	}
}

func TestSetHeaderBytesCopiesTheValue(t *testing.T) {
	req := "GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
	val := []byte("abc-123")
	got := serveHeaders(t, req, func(ctx *Ctx) {
		if err := ctx.SetHeaderBytes("X-Request-Id", val); err != nil {
			t.Fatal(err)
		}
		val[0] = 'Z'
		ctx.SetBodyString("ok")
	})
	if !bytes.Contains(got, []byte("X-Request-Id: abc-123\r\n")) {
		t.Fatalf("value was not copied: %q", got)
	}
	if bytes.Contains(got, []byte("X-Request-Id: Zbc-123\r\n")) {
		t.Fatalf("mutated source leaked: %q", got)
	}
}

func TestSetHeaderBytesRejectsCRLF(t *testing.T) {
	ctx := &Ctx{}
	if err := ctx.SetHeaderBytes("X-Request-Id", []byte("a\r\nb")); !errors.Is(err, ErrHeaderInvalid) {
		t.Fatalf("err=%v", err)
	}
	if len(ctx.respHeaderBuf) != 0 {
		t.Fatalf("buffer=%q", ctx.respHeaderBuf)
	}
	if err := ctx.SetHeader("X-Request-Id", "a\r\nb"); !errors.Is(err, ErrHeaderInvalid) {
		t.Fatalf("string err=%v", err)
	}
}

func serveHeaders(t *testing.T, req string, h Handler) []byte {
	t.Helper()
	srv := noDeadlineServer(h)
	srv.NoDefaultDate = true
	fc := &scriptConn{parts: [][]byte{[]byte(req)}}
	if err := srv.ServeConn(fc); err != nil && !strings.Contains(err.Error(), "EOF") {
		t.Fatal(err)
	}
	return fc.w.Bytes()
}
