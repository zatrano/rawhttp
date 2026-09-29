package rawhttp

import (
	"bytes"
	"strconv"
	"time"
)

// Cookie holds a Set-Cookie / Cookie name-value pair and common attributes.
type Cookie struct {
	Name     string
	Value    string
	Path     string
	Domain   string
	Expires  time.Time
	MaxAge   int // seconds; 0 means omit; <0 means delete
	Secure   bool
	HTTPOnly bool
	SameSite SameSite
}

// SameSite cookie attribute.
type SameSite int

const (
	SameSiteDefaultMode SameSite = iota
	SameSiteLaxMode
	SameSiteStrictMode
	SameSiteNoneMode
)

// Cookie returns the named request cookie value, or nil if absent.
func (c *Ctx) Cookie(name string) []byte {
	raw := c.cookieHdr
	if len(raw) == 0 || len(name) == 0 {
		return nil
	}
	off := 0
	for off <= len(raw) {
		end := off
		for end < len(raw) && raw[end] != ';' {
			end++
		}
		part := raw[off:end]
		for len(part) > 0 && (part[0] == ' ' || part[0] == '\t') {
			part = part[1:]
		}
		eq := bytes.IndexByte(part, '=')
		var k, v []byte
		if eq < 0 {
			k = part
		} else {
			k = part[:eq]
			v = part[eq+1:]
		}
		if len(k) == len(name) && equalFoldStr(k, name) {
			return v
		}
		if end >= len(raw) {
			break
		}
		off = end + 1
	}
	return nil
}

// VisitCookie calls f for each name/value pair in the Cookie request header.
func (c *Ctx) VisitCookie(f func(name, val []byte)) {
	raw := c.cookieHdr
	if len(raw) == 0 {
		return
	}
	off := 0
	for off <= len(raw) {
		end := off
		for end < len(raw) && raw[end] != ';' {
			end++
		}
		part := raw[off:end]
		for len(part) > 0 && (part[0] == ' ' || part[0] == '\t') {
			part = part[1:]
		}
		eq := bytes.IndexByte(part, '=')
		var k, v []byte
		if eq < 0 {
			k = part
		} else {
			k = part[:eq]
			v = part[eq+1:]
		}
		if len(k) > 0 {
			f(k, v)
		}
		if end >= len(raw) {
			break
		}
		off = end + 1
	}
}

// SetCookie appends a Set-Cookie response header.
func (c *Ctx) SetCookie(cookie *Cookie) error {
	c.lockResp()
	defer c.unlockResp()
	if c.timedOut {
		return nil
	}
	if cookie == nil || cookie.Name == "" || !validHeaderNameStr(cookie.Name) {
		return ErrHeaderInvalid
	}
	if containsCTLOrCRLF(cookie.Value) || containsCTLOrCRLF(cookie.Path) || containsCTLOrCRLF(cookie.Domain) {
		return ErrHeaderInvalid
	}
	c.respHeaderBuf = append(c.respHeaderBuf, "Set-Cookie: "...)
	c.respHeaderBuf = append(c.respHeaderBuf, cookie.Name...)
	c.respHeaderBuf = append(c.respHeaderBuf, '=')
	c.respHeaderBuf = append(c.respHeaderBuf, cookie.Value...)
	if cookie.Path != "" {
		c.respHeaderBuf = append(c.respHeaderBuf, "; Path="...)
		c.respHeaderBuf = append(c.respHeaderBuf, cookie.Path...)
	}
	if cookie.Domain != "" {
		c.respHeaderBuf = append(c.respHeaderBuf, "; Domain="...)
		c.respHeaderBuf = append(c.respHeaderBuf, cookie.Domain...)
	}
	if cookie.MaxAge > 0 {
		c.respHeaderBuf = append(c.respHeaderBuf, "; Max-Age="...)
		c.respHeaderBuf = strconv.AppendInt(c.respHeaderBuf, int64(cookie.MaxAge), 10)
	} else if cookie.MaxAge < 0 {
		c.respHeaderBuf = append(c.respHeaderBuf, "; Max-Age=0"...)
	}
	if !cookie.Expires.IsZero() {
		c.respHeaderBuf = append(c.respHeaderBuf, "; Expires="...)
		c.respHeaderBuf = appendHTTPDate(c.respHeaderBuf, cookie.Expires.UTC())
	}
	if cookie.HTTPOnly {
		c.respHeaderBuf = append(c.respHeaderBuf, "; HttpOnly"...)
	}
	secure := cookie.Secure
	if cookie.SameSite == SameSiteNoneMode {
		secure = true // SameSite=None requires Secure
	}
	if secure {
		c.respHeaderBuf = append(c.respHeaderBuf, "; Secure"...)
	}
	switch cookie.SameSite {
	case SameSiteLaxMode:
		c.respHeaderBuf = append(c.respHeaderBuf, "; SameSite=Lax"...)
	case SameSiteStrictMode:
		c.respHeaderBuf = append(c.respHeaderBuf, "; SameSite=Strict"...)
	case SameSiteNoneMode:
		c.respHeaderBuf = append(c.respHeaderBuf, "; SameSite=None"...)
	}
	c.respHeaderBuf = append(c.respHeaderBuf, '\r', '\n')
	c.cacheOK = false
	return nil
}

// DeleteCookie expires the named cookie (Max-Age=0, Path=/).
func (c *Ctx) DeleteCookie(name string) error {
	return c.SetCookie(&Cookie{Name: name, MaxAge: -1, Path: "/"})
}

// SecureCookie sets a cookie with Secure, HttpOnly, and SameSite=Lax defaults.
func (c *Ctx) SecureCookie(name, value string) error {
	return c.SetCookie(&Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		Secure:   true,
		HTTPOnly: true,
		SameSite: SameSiteLaxMode,
	})
}
