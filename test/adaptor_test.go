package test_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/zatrano/rawhttp"
)

func TestAdaptor_Handler(t *testing.T) {
	h := rawhttp.AdaptHandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/hello" {
			t.Errorf("path=%q", r.URL.Path)
		}
		w.Header().Set("X-From", "nethttp")
		w.WriteHeader(201)
		_, _ = w.Write([]byte("adapted"))
	})
	srv := &rawhttp.Server{Handler: h}
	fc := newFakeConn([]byte("GET /hello HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"))
	if err := srv.ServeConn(fc); err != nil {
		t.Fatal(err)
	}
	resp := fc.w.String()
	if !strings.Contains(resp, "201") {
		t.Fatalf("status: %q", resp)
	}
	if !strings.Contains(resp, "X-From: nethttp\r\n") {
		t.Fatalf("header: %q", resp)
	}
	if !strings.HasSuffix(resp, "adapted") {
		t.Fatalf("body: %q", resp)
	}
}
