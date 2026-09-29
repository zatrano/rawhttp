package rawhttp

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"time"
)

// BasicAuthMiddleware requires HTTP Basic credentials matching user/pass.
// Comparison is constant-time. realm is used in WWW-Authenticate (empty → "Restricted").
func BasicAuthMiddleware(h Handler, user, pass, realm string) Handler {
	want := []byte(user + ":" + pass)
	return func(ctx *Ctx) {
		u, p, ok := ctx.BasicAuth()
		got := []byte(u + ":" + p)
		if !ok || subtle.ConstantTimeCompare(got, want) != 1 {
			ctx.SetBasicAuthChallenge(realm)
			return
		}
		h(ctx)
	}
}

// RequestIDOptions configures RequestIDMiddleware.
type RequestIDOptions struct {
	// Header is the request/response header name. Empty → "X-Request-ID".
	Header string
	// TrustClient, when true, reuses a valid incoming header value (ASCII token, ≤128).
	// Default false: always generate a new id (safer behind untrusted clients).
	TrustClient bool
}

// RequestIDMiddleware ensures a request id header on the response (and stores it as UserValue).
func RequestIDMiddleware(h Handler, opt RequestIDOptions) Handler {
	hdr := opt.Header
	if hdr == "" {
		hdr = "X-Request-ID"
	}
	return func(ctx *Ctx) {
		id := ""
		if opt.TrustClient {
			if v := ctx.Header(hdr); len(v) > 0 && validRequestID(v) {
				id = string(v)
			}
		}
		if id == "" {
			id = newRequestID()
		}
		ctx.SetUserValue(hdr, id)
		_ = ctx.SetHeader(hdr, id)
		h(ctx)
	}
}

func validRequestID(v []byte) bool {
	if len(v) == 0 || len(v) > 128 {
		return false
	}
	for _, c := range v {
		if c < 0x21 || c > 0x7e || c == '"' || c == '\\' {
			return false
		}
	}
	return true
}

func newRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Extremely unlikely; fall back to timestamp hex.
		n := time.Now().UnixNano()
		return hex.EncodeToString([]byte{
			byte(n >> 56), byte(n >> 48), byte(n >> 40), byte(n >> 32),
			byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n),
		})
	}
	return hex.EncodeToString(b[:])
}

// AccessLogFunc is called after the handler returns with the final status code.
type AccessLogFunc func(ctx *Ctx, status int, dur time.Duration)

// AccessLogMiddleware times the handler and invokes logFn afterward.
func AccessLogMiddleware(h Handler, logFn AccessLogFunc) Handler {
	if logFn == nil {
		return h
	}
	return func(ctx *Ctx) {
		start := time.Now()
		h(ctx)
		logFn(ctx, ctx.StatusCode, time.Since(start))
	}
}

// StripPrefixMiddleware strips prefix from ctx.Path before calling h.
// If the path does not have the prefix, responds 404.
func StripPrefixMiddleware(prefix string, h Handler) Handler {
	if prefix == "" || prefix == "/" {
		return h
	}
	return func(ctx *Ctx) {
		if !ctx.PathHasPrefix(prefix) {
			ctx.NotFound()
			return
		}
		rest := ctx.Path[len(prefix):]
		if len(rest) == 0 {
			rest = []byte("/")
		} else if rest[0] != '/' {
			// "/api" + "v1" → reject (must be "/api/" or exact "/api")
			ctx.NotFound()
			return
		}
		ctx.Path = rest
		h(ctx)
	}
}

// MethodOverrideMiddleware rewrites POST to the method in
// X-HTTP-Method-Override (or form field "_method") when present and safe
// (GET/HEAD/PUT/PATCH/DELETE). Only applies to POST requests.
func MethodOverrideMiddleware(h Handler) Handler {
	return func(ctx *Ctx) {
		if equalFoldStr(ctx.Method, "POST") {
			m := ctx.Header("X-HTTP-Method-Override")
			if len(m) == 0 {
				m = ctx.FormValue("_method")
			}
			if len(m) > 0 && allowedOverrideMethod(m) {
				ctx.methodCopy = append(ctx.methodCopy[:0], m...)
				ctx.Method = ctx.methodCopy
				ctx.head = equalFoldStr(ctx.Method, "HEAD")
			}
		}
		h(ctx)
	}
}

func allowedOverrideMethod(m []byte) bool {
	return equalFoldStr(m, "GET") || equalFoldStr(m, "HEAD") ||
		equalFoldStr(m, "PUT") || equalFoldStr(m, "PATCH") || equalFoldStr(m, "DELETE")
}

// NoCacheMiddleware sets Cache-Control: no-store after the handler.
func NoCacheMiddleware(h Handler) Handler {
	return func(ctx *Ctx) {
		h(ctx)
		_ = ctx.SetHeader("Cache-Control", "no-store")
	}
}

// TimeoutMiddleware is an alias for TimeoutHandler (middleware naming).
func TimeoutMiddleware(h Handler, timeout time.Duration, msg string) Handler {
	return TimeoutHandler(h, timeout, msg)
}

// HeadOrGetMiddleware rejects methods other than GET/HEAD with 405.
func HeadOrGetMiddleware(h Handler) Handler {
	return func(ctx *Ctx) {
		if equalFoldStr(ctx.Method, "GET") || equalFoldStr(ctx.Method, "HEAD") {
			h(ctx)
			return
		}
		_ = ctx.SetHeader("Allow", "GET, HEAD")
		ctx.Error("Method Not Allowed", 405)
	}
}

// NormalizePathMiddleware collapses duplicate slashes in ctx.Path ("//a//b" → "/a/b").
// Does not resolve ".." (already rejected by the request parser).
func NormalizePathMiddleware(h Handler) Handler {
	return func(ctx *Ctx) {
		p := ctx.Path
		if len(p) > 1 {
			dst := 1
			for i := 1; i < len(p); i++ {
				if p[i] == '/' && p[dst-1] == '/' {
					continue
				}
				if dst != i {
					p[dst] = p[i]
				}
				dst++
			}
			ctx.Path = p[:dst]
		}
		h(ctx)
	}
}

// RequireContentTypeMiddleware rejects requests whose Content-Type does not
// match want (case-insensitive prefix before ';') with 415.
func RequireContentTypeMiddleware(want string, h Handler) Handler {
	return func(ctx *Ctx) {
		ct := ctx.RequestContentType()
		if !contentTypeMatches(ct, want) {
			ctx.Error("Unsupported Media Type", 415)
			return
		}
		h(ctx)
	}
}

func contentTypeMatches(ct []byte, want string) bool {
	if want == "" {
		return true
	}
	// Strip parameters after ';'
	n := len(ct)
	for i := 0; i < n; i++ {
		if ct[i] == ';' {
			n = i
			break
		}
	}
	for n > 0 && (ct[n-1] == ' ' || ct[n-1] == '\t') {
		n--
	}
	return equalFoldStr(ct[:n], want)
}

// RequireMethodsMiddleware rejects methods not in methods with 405 + Allow.
func RequireMethodsMiddleware(methods []string, h Handler) Handler {
	allow := ""
	for i, m := range methods {
		if i > 0 {
			allow += ", "
		}
		allow += m
	}
	return func(ctx *Ctx) {
		for _, m := range methods {
			if equalFoldStr(ctx.Method, m) {
				h(ctx)
				return
			}
		}
		_ = ctx.MethodNotAllowed(allow)
	}
}

// HTTPSRedirectMiddleware redirects cleartext requests to https://Host+path
// with 308. TLS requests pass through to h.
func HTTPSRedirectMiddleware(h Handler) Handler {
	return func(ctx *Ctx) {
		if ctx.IsTLS() {
			h(ctx)
			return
		}
		host := ctx.Host()
		if len(host) == 0 {
			ctx.Error("Missing Host", 400)
			return
		}
		url := make([]byte, 0, 8+len(host)+len(ctx.Path)+1+len(ctx.Query))
		url = append(url, "https://"...)
		url = append(url, host...)
		if len(ctx.Path) == 0 {
			url = append(url, '/')
		} else {
			url = append(url, ctx.Path...)
		}
		if len(ctx.Query) > 0 {
			url = append(url, '?')
			url = append(url, ctx.Query...)
		}
		_ = ctx.Redirect(string(url), 308)
	}
}
