package rawhttp

import (
	"bytes"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// AdaptHandler converts an http.Handler into a rawhttp Handler.
// This path allocates and is slower than native handlers; use it for
// migration, not for the hot path.
func AdaptHandler(h http.Handler) Handler {
	return func(ctx *Ctx) {
		req, err := requestFromCtx(ctx)
		if err != nil {
			ctx.BadRequest()
			return
		}
		rw := &netHTTPResponseWriter{ctx: ctx, header: make(http.Header)}
		h.ServeHTTP(rw, req)
		rw.finish()
	}
}

// AdaptHandlerFunc converts an http.HandlerFunc into a rawhttp Handler.
func AdaptHandlerFunc(f http.HandlerFunc) Handler {
	return AdaptHandler(f)
}

func requestFromCtx(ctx *Ctx) (*http.Request, error) {
	u := &url.URL{Path: string(ctx.Path)}
	if q := ctx.Query; len(q) > 0 {
		u.RawQuery = string(q)
	}
	var body io.ReadCloser = http.NoBody
	if b := ctx.Body(); len(b) > 0 {
		body = io.NopCloser(bytes.NewReader(b))
	}
	req, err := http.NewRequest(string(ctx.Method), u.String(), body)
	if err != nil {
		return nil, err
	}
	req.Host = string(ctx.Host())
	req.RequestURI = string(ctx.RequestURI())
	req.RemoteAddr = ctx.RemoteAddr()
	ctx.VisitHeader(func(k, v []byte) {
		req.Header.Add(string(k), string(v))
	})
	if ctx.IsTLS() {
		req.TLS = nil // presence via scheme only; full state unavailable
		req.URL.Scheme = "https"
	} else {
		req.URL.Scheme = "http"
	}
	return req, nil
}

type netHTTPResponseWriter struct {
	ctx         *Ctx
	header      http.Header
	wroteHeader bool
	code        int
	buf         bytes.Buffer
}

func (w *netHTTPResponseWriter) Header() http.Header { return w.header }

func (w *netHTTPResponseWriter) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.buf.Write(p)
}

func (w *netHTTPResponseWriter) WriteHeader(code int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.code = code
}

func (w *netHTTPResponseWriter) finish() {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	w.ctx.SetStatusCode(w.code)
	for k, vals := range w.header {
		lk := strings.ToLower(k)
		if lk == "content-length" || lk == "transfer-encoding" || lk == "connection" {
			continue
		}
		if lk == "content-type" && len(vals) > 0 {
			w.ctx.SetContentType(vals[0])
			continue
		}
		for _, v := range vals {
			_ = w.ctx.AddHeader(k, v)
		}
	}
	w.ctx.SetBody(w.buf.Bytes())
}
