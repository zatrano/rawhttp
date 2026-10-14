package rawhttp

import (
	"strings"
	"testing"
)

func TestBufferedBodyIsAView(t *testing.T) {
	var bodyCap int
	srv := noDeadlineServer(func(ctx *Ctx) {
		bodyCap = cap(ctx.Body())
		ctx.SetBodyString("ok")
	})
	body := "0123456789abcdef"
	req := "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 16\r\nConnection: close\r\n\r\n" + body
	fc := &scriptConn{parts: [][]byte{[]byte(req)}}
	if err := srv.ServeConn(fc); err != nil && !strings.Contains(err.Error(), "EOF") {
		t.Fatal(err)
	}
	if bodyCap < 512 {
		t.Fatalf("body cap=%d, want a view of the read buffer", bodyCap)
	}
	if !strings.Contains(fc.String(), "200") {
		t.Fatalf("resp=%q", fc.String())
	}
}

func TestEchoViewSurvivesPipeline(t *testing.T) {
	srv := noDeadlineServer(func(ctx *Ctx) {
		ctx.SetBody(ctx.Body())
		ctx.SetContentType("text/plain")
	})
	req := "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 4\r\n\r\nabcd" +
		"POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 3\r\nConnection: close\r\n\r\nxyz"
	fc := &scriptConn{parts: [][]byte{[]byte(req)}}
	if err := srv.ServeConn(fc); err != nil && !strings.Contains(err.Error(), "EOF") {
		t.Fatal(err)
	}
	got := fc.String()
	if !strings.Contains(got, "abcd") || !strings.Contains(got, "xyz") {
		t.Fatalf("resp=%q", got)
	}
}
