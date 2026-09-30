//go:build !rawhttp_poison

package test_test

import (
	"testing"

	"github.com/zatrano/rawhttp"
)

func TestPoison_BuildDisabled(t *testing.T) {
	if rawhttp.PoisonBuildEnabled() {
		t.Fatal("PoisonBuildEnabled must be false without -tags rawhttp_poison")
	}
}

func TestPoisonDisabled_RetainedMethodNotFilled(t *testing.T) {
	// Without the tag, a single-request retain must not see the 0xDE fill
	// (poisonAfterHandler is a no-op).
	var held []byte
	srv := &rawhttp.Server{
		ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
		Handler: func(ctx *rawhttp.Ctx) {
			held = ctx.Method
			ctx.SetBody([]byte("ok"))
		},
	}
	fc := newFakeConn([]byte("GET / HTTP/1.1\r\nHost: h\r\nConnection: close\r\n\r\n"))
	_ = srv.ServeConn(fc)
	if string(held) != "GET" {
		t.Fatalf("without poison tag want Method GET, got %q", held)
	}
	for _, c := range held {
		if c == 0xDE {
			t.Fatalf("0xDE fill must not run without rawhttp_poison tag")
		}
	}
}
