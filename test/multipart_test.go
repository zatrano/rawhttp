package test_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zatrano/rawhttp"
)

func TestMultipartFormAndFile(t *testing.T) {
	boundary := "----rawhttpboundary"
	body := strings.Join([]string{
		"--" + boundary,
		`Content-Disposition: form-data; name="title"`,
		"",
		"hello",
		"--" + boundary,
		`Content-Disposition: form-data; name="file"; filename="a.txt"`,
		"Content-Type: text/plain",
		"",
		"file-bytes",
		"--" + boundary + "--",
		"",
	}, "\r\n")
	req := fmt.Sprintf(
		"POST /upload HTTP/1.1\r\nHost: localhost\r\nContent-Type: multipart/form-data; boundary=%s\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s",
		boundary, len(body), body,
	)

	var gotTitle, gotName, gotFile string
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) {
		mf, err := ctx.MultipartForm(0)
		if err != nil {
			t.Errorf("multipart: %v", err)
			ctx.BadRequest()
			return
		}
		gotTitle = ctx.MultipartValue("title")
		fh, err := ctx.FormFile("file")
		if err != nil {
			t.Errorf("file: %v", err)
			return
		}
		gotName = fh.Filename
		data, err := rawhttp.ReadMultipartFile(fh, 1<<20)
		if err != nil {
			t.Errorf("read: %v", err)
			return
		}
		gotFile = string(data)
		_ = mf
		ctx.SetBody([]byte("ok"))
	}}
	fc := newFakeConn([]byte(req))
	if err := srv.ServeConn(fc); err != nil {
		t.Fatal(err)
	}
	if gotTitle != "hello" || gotName != "a.txt" || gotFile != "file-bytes" {
		t.Fatalf("title=%q name=%q file=%q", gotTitle, gotName, gotFile)
	}
	if !strings.Contains(fc.w.String(), "ok") {
		t.Fatalf("resp=%q", fc.w.String())
	}
}

func TestPipelineAndLBClient(t *testing.T) {
	addr, stop := startTestServer(t, func(ctx *rawhttp.Ctx) {
		ctx.SetBody([]byte("p"))
	})
	defer stop()

	pc := &rawhttp.PipelineClient{Addr: addr, MaxPendingRequests: 8}
	req := rawhttp.AcquireRequest()
	defer rawhttp.ReleaseRequest(req)
	req.RequestURI = "/"
	resp := rawhttp.AcquireResponse()
	defer rawhttp.ReleaseResponse(resp)
	if err := pc.Do(req, resp); err != nil {
		t.Fatal(err)
	}
	if string(resp.Body()) != "p" {
		t.Fatalf("pipeline body=%q", resp.Body())
	}
	pc.CloseIdleConnections()

	lb := &rawhttp.LBClient{
		Clients: []*rawhttp.HostClient{
			{Addr: addr},
			{Addr: addr},
		},
	}
	resp.Reset()
	if err := lb.Do(req, resp); err != nil {
		t.Fatal(err)
	}
	if string(resp.Body()) != "p" {
		t.Fatalf("lb body=%q", resp.Body())
	}
	lb.CloseIdleConnections()
}

func TestPipelinePendingLimit(t *testing.T) {
	addr, stop := startTestServer(t, func(ctx *rawhttp.Ctx) {
		time.Sleep(80 * time.Millisecond)
		ctx.SetBody([]byte("ok"))
	})
	defer stop()

	pc := &rawhttp.PipelineClient{Addr: addr, MaxPendingRequests: 1, MaxConns: 2}
	defer pc.CloseIdleConnections()

	started := make(chan struct{})
	errCh := make(chan error, 1)
	go func() {
		req := rawhttp.AcquireRequest()
		resp := rawhttp.AcquireResponse()
		defer rawhttp.ReleaseRequest(req)
		defer rawhttp.ReleaseResponse(resp)
		req.RequestURI = "/"
		close(started)
		errCh <- pc.Do(req, resp)
	}()
	<-started
	time.Sleep(20 * time.Millisecond)

	req := rawhttp.AcquireRequest()
	resp := rawhttp.AcquireResponse()
	defer rawhttp.ReleaseRequest(req)
	defer rawhttp.ReleaseResponse(resp)
	req.RequestURI = "/"
	err := pc.Do(req, resp)
	if err == nil || !strings.Contains(err.Error(), "pending limit") {
		t.Fatalf("want pending limit error, got %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("first Do: %v", err)
	}
}

func TestMultipartBoundaryTooLong(t *testing.T) {
	boundary := strings.Repeat("b", 80)
	body := "--" + boundary + "\r\nContent-Disposition: form-data; name=\"a\"\r\n\r\nx\r\n--" + boundary + "--\r\n"
	req := fmt.Sprintf(
		"POST /upload HTTP/1.1\r\nHost: localhost\r\nContent-Type: multipart/form-data; boundary=%s\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s",
		boundary, len(body), body,
	)
	data := []byte(req)
	fc := newFakeConn(data)
	srv := &rawhttp.Server{
		ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
		Handler: func(ctx *rawhttp.Ctx) {
			_, err := ctx.MultipartForm(0)
			if err == nil {
				t.Error("expected ErrBadRequest for long boundary")
			}
			ctx.SetStatusCode(400)
			ctx.SetBody([]byte("bad"))
		},
	}
	_ = srv.ServeConn(fc)
}

func TestSaveMultipartFile(t *testing.T) {
	boundary := "----saveboundary"
	body := strings.Join([]string{
		"--" + boundary,
		`Content-Disposition: form-data; name="file"; filename="a.txt"`,
		"Content-Type: text/plain",
		"",
		"saved-bytes",
		"--" + boundary + "--",
		"",
	}, "\r\n")
	req := fmt.Sprintf(
		"POST /upload HTTP/1.1\r\nHost: localhost\r\nContent-Type: multipart/form-data; boundary=%s\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s",
		boundary, len(body), body,
	)
	dst := filepath.Join(t.TempDir(), "out.txt")
	srv := &rawhttp.Server{Handler: func(ctx *rawhttp.Ctx) {
		fh, err := ctx.FormFile("file")
		if err != nil {
			t.Errorf("file: %v", err)
			return
		}
		if err := rawhttp.SaveMultipartFile(fh, dst, 1<<20); err != nil {
			t.Errorf("save: %v", err)
			return
		}
		ctx.SetBody([]byte("ok"))
	}}
	fc := newFakeConn([]byte(req))
	_ = srv.ServeConn(fc)
	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "saved-bytes" {
		t.Fatalf("got %q", data)
	}
}

func TestMaxMultipartFiles(t *testing.T) {
	boundary := "----limboundary"
	body := strings.Join([]string{
		"--" + boundary,
		`Content-Disposition: form-data; name="f1"; filename="a.txt"`,
		"Content-Type: text/plain",
		"",
		"a",
		"--" + boundary,
		`Content-Disposition: form-data; name="f2"; filename="b.txt"`,
		"Content-Type: text/plain",
		"",
		"b",
		"--" + boundary + "--",
		"",
	}, "\r\n")
	req := fmt.Sprintf(
		"POST /upload HTTP/1.1\r\nHost: localhost\r\nContent-Type: multipart/form-data; boundary=%s\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s",
		boundary, len(body), body,
	)
	srv := &rawhttp.Server{
		MaxMultipartFiles: 1,
		Handler: func(ctx *rawhttp.Ctx) {
			_, err := ctx.MultipartForm(0)
			if err == nil {
				t.Error("expected ErrBadRequest for too many files")
			}
			ctx.SetStatusCode(400)
			ctx.SetBody([]byte("bad"))
		},
	}
	fc := newFakeConn([]byte(req))
	_ = srv.ServeConn(fc)
	if !strings.Contains(fc.w.String(), "400") {
		t.Fatalf("want 400: %q", fc.w.String())
	}
}
