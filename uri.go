package rawhttp

import (
	"bytes"
)

// URI holds parsed components of a request or absolute URL.
// Byte slices returned by getters are owned by URI until the next mutate/Reset.
type URI struct {
	scheme   []byte
	host     []byte
	path     []byte
	query    []byte
	hash     []byte
	username []byte
	password []byte
}

// Reset clears all URI components.
func (u *URI) Reset() {
	u.scheme = u.scheme[:0]
	u.host = u.host[:0]
	u.path = u.path[:0]
	u.query = u.query[:0]
	u.hash = u.hash[:0]
	u.username = u.username[:0]
	u.password = u.password[:0]
}

// Scheme returns the URI scheme (e.g. "http").
func (u *URI) Scheme() []byte { return u.scheme }

// SetScheme sets the scheme.
func (u *URI) SetScheme(s string) { u.scheme = append(u.scheme[:0], s...) }

// Host returns host[:port].
func (u *URI) Host() []byte { return u.host }

// SetHost sets host[:port].
func (u *URI) SetHost(s string) { u.host = append(u.host[:0], s...) }

// Path returns the path (defaults to "/").
func (u *URI) Path() []byte {
	if len(u.path) == 0 {
		return []byte{'/'}
	}
	return u.path
}

// SetPath sets the path.
func (u *URI) SetPath(s string) { u.path = append(u.path[:0], s...) }

// QueryString returns the raw query without leading '?'.
func (u *URI) QueryString() []byte { return u.query }

// SetQueryString sets the raw query (without '?').
func (u *URI) SetQueryString(s string) { u.query = append(u.query[:0], s...) }

// Hash returns the fragment without leading '#'.
func (u *URI) Hash() []byte { return u.hash }

// SetHash sets the fragment.
func (u *URI) SetHash(s string) { u.hash = append(u.hash[:0], s...) }

// Username returns userinfo username.
func (u *URI) Username() []byte { return u.username }

// Password returns userinfo password.
func (u *URI) Password() []byte { return u.password }

// SetUsername sets the username.
func (u *URI) SetUsername(s string) { u.username = append(u.username[:0], s...) }

// SetPassword sets the password.
func (u *URI) SetPassword(s string) { u.password = append(u.password[:0], s...) }

// QueryArgs returns a read-only view of the query string.
func (u *URI) QueryArgs() QueryArgs { return QueryArgs{raw: u.query} }

// RequestURI returns path + optional "?" + query (origin-form).
func (u *URI) RequestURI() []byte {
	p := u.Path()
	if len(u.query) == 0 {
		return p
	}
	buf := make([]byte, 0, len(p)+1+len(u.query))
	buf = append(buf, p...)
	buf = append(buf, '?')
	buf = append(buf, u.query...)
	return buf
}

// FullURI returns scheme://host/path?query#hash when scheme/host are set.
func (u *URI) FullURI() []byte {
	var buf []byte
	if len(u.scheme) > 0 {
		buf = append(buf, u.scheme...)
		buf = append(buf, "://"...)
	}
	if len(u.username) > 0 || len(u.password) > 0 {
		buf = append(buf, u.username...)
		if len(u.password) > 0 {
			buf = append(buf, ':')
			buf = append(buf, u.password...)
		}
		buf = append(buf, '@')
	}
	buf = append(buf, u.host...)
	buf = append(buf, u.RequestURI()...)
	if len(u.hash) > 0 {
		buf = append(buf, '#')
		buf = append(buf, u.hash...)
	}
	return buf
}

// Parse copies an absolute or origin-form URI string into u.
func (u *URI) Parse(s string) {
	u.Reset()
	b := []byte(s)
	// fragment
	if i := bytes.IndexByte(b, '#'); i >= 0 {
		u.hash = append(u.hash[:0], b[i+1:]...)
		b = b[:i]
	}
	// scheme://
	if i := bytes.Index(b, []byte("://")); i > 0 {
		u.scheme = append(u.scheme[:0], b[:i]...)
		b = b[i+3:]
		// authority
		slash := bytes.IndexByte(b, '/')
		q := bytes.IndexByte(b, '?')
		end := len(b)
		if slash >= 0 && slash < end {
			end = slash
		}
		if q >= 0 && q < end {
			end = q
		}
		auth := b[:end]
		b = b[end:]
		if at := bytes.LastIndexByte(auth, '@'); at >= 0 {
			userinfo := auth[:at]
			auth = auth[at+1:]
			if colon := bytes.IndexByte(userinfo, ':'); colon >= 0 {
				u.username = append(u.username[:0], userinfo[:colon]...)
				u.password = append(u.password[:0], userinfo[colon+1:]...)
			} else {
				u.username = append(u.username[:0], userinfo...)
			}
		}
		u.host = append(u.host[:0], auth...)
	}
	// path?query
	if q := bytes.IndexByte(b, '?'); q >= 0 {
		u.path = append(u.path[:0], b[:q]...)
		u.query = append(u.query[:0], b[q+1:]...)
	} else {
		u.path = append(u.path[:0], b...)
	}
	if len(u.path) == 0 && len(u.scheme) > 0 {
		u.path = append(u.path[:0], '/')
	}
}

// UpdateFromCtx fills u from the current request (Host + Path + Query).
func (u *URI) UpdateFromCtx(c *Ctx) {
	u.Reset()
	if c.isTLS {
		u.scheme = append(u.scheme[:0], "https"...)
	} else {
		u.scheme = append(u.scheme[:0], "http"...)
	}
	u.host = append(u.host[:0], c.host...)
	u.path = append(u.path[:0], c.Path...)
	u.query = append(u.query[:0], c.Query...)
}

// URI returns a request URI view for the current request.
// The returned pointer is valid until the next call or request reset.
func (c *Ctx) URI() *URI {
	if c.uri == nil {
		c.uri = &URI{}
	}
	c.uri.UpdateFromCtx(c)
	return c.uri
}
