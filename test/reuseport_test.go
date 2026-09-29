package test_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/zatrano/rawhttp"
)

func TestReusePort_ListenAndServe(t *testing.T) {
	ln, err := rawhttp.ListenReusePort("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) {
		ctx.SetBody([]byte("ok"))
	}}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	addr := "http://" + ln.Addr().String() + "/"
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}
