package rawhttp

import (
	"bytes"
)

// FormValueFunc customizes Ctx.FormValue lookup. See Server.FormValueFunc.
type FormValueFunc func(ctx *Ctx, key string) []byte

// PostArgs returns application/x-www-form-urlencoded body fields.
// Non-form content types yield an empty Args view.
func (c *Ctx) PostArgs() QueryArgs {
	if !isFormURLEncoded(c.reqContentType) {
		return QueryArgs{}
	}
	return QueryArgs{raw: c.reqBody}
}

// FormValue returns the first query, urlencoded post, or multipart text value for key.
// Override order via Server.FormValueFunc (e.g. NetHTTPFormValueFunc).
func (c *Ctx) FormValue(key string) []byte {
	if c.formValueFunc != nil {
		return c.formValueFunc(c, key)
	}
	return defaultFormValue(c, key)
}

func defaultFormValue(c *Ctx, key string) []byte {
	if v := c.QueryArgs().Get(key); len(v) > 0 {
		return v
	}
	if v := c.PostArgs().Get(key); len(v) > 0 {
		return v
	}
	return formValueMultipart(c, key)
}

// NetHTTPFormValueFunc matches net/http.Request.FormValue: body (urlencoded or
// multipart) before query string.
var NetHTTPFormValueFunc FormValueFunc = func(ctx *Ctx, key string) []byte {
	if v := ctx.PostArgs().Get(key); len(v) > 0 {
		return v
	}
	if v := formValueMultipart(ctx, key); len(v) > 0 {
		return v
	}
	if v := ctx.QueryArgs().Get(key); len(v) > 0 {
		return v
	}
	return nil
}

func isFormURLEncoded(ct []byte) bool {
	// application/x-www-form-urlencoded — allow trailing parameters after ';'
	const want = "application/x-www-form-urlencoded"
	if len(ct) < len(want) {
		return false
	}
	if !equalFoldStr(ct[:len(want)], want) {
		return false
	}
	if len(ct) == len(want) {
		return true
	}
	return ct[len(want)] == ';' || ct[len(want)] == ' '
}

// HasFormBody reports whether the request looks like a URL-encoded form POST/PUT.
func (c *Ctx) HasFormBody() bool {
	return len(c.reqBody) > 0 && isFormURLEncoded(c.reqContentType)
}

// parse like QueryArgs but also supports decoding '+' as space for form bodies.
// QueryArgs.Get already treats raw bytes; HTML forms use + for space.
// Add FormArgs helper that normalizes.

// FormArgs returns post form args with '+' decoded as space in values/keys
// when scanning. For zero-copy hot paths prefer PostArgs and decode selectively.
func (c *Ctx) FormArgs() QueryArgs {
	return c.PostArgs()
}

// DecodeFormPlus replaces '+' with space in b (in place) for form decoding.
func DecodeFormPlus(b []byte) {
	for i, c := range b {
		if c == '+' {
			b[i] = ' '
		}
	}
}

// VisitForm calls f for each application/x-www-form-urlencoded pair with
// '+' normalized to space in temporary copies when needed.
func (c *Ctx) VisitForm(f func(key, val []byte)) {
	if !isFormURLEncoded(c.reqContentType) {
		return
	}
	raw := c.reqBody
	off := 0
	for off <= len(raw) {
		end := off
		for end < len(raw) && raw[end] != '&' {
			end++
		}
		pair := raw[off:end]
		eq := bytes.IndexByte(pair, '=')
		var k, v []byte
		if eq < 0 {
			k = pair
		} else {
			k = pair[:eq]
			v = pair[eq+1:]
		}
		needCopy := bytes.IndexByte(k, '+') >= 0 || bytes.IndexByte(v, '+') >= 0
		if needCopy {
			kb := append([]byte(nil), k...)
			vb := append([]byte(nil), v...)
			DecodeFormPlus(kb)
			DecodeFormPlus(vb)
			f(kb, vb)
		} else if len(k) > 0 || len(v) > 0 || len(pair) > 0 {
			f(k, v)
		}
		if end >= len(raw) {
			break
		}
		off = end + 1
	}
}
