package rawhttp

import (
	"bytes"
	"net/url"
	"strconv"
)

// Args is a mutable collection of key/value pairs (query string or form body).
type Args struct {
	buf []byte
}

// Reset clears all args.
func (a *Args) Reset() { a.buf = a.buf[:0] }

// Parse replaces args with the decoded query/form string s.
func (a *Args) Parse(s string) {
	a.buf = append(a.buf[:0], s...)
}

// ParseBytes replaces args with b (copied).
func (a *Args) ParseBytes(b []byte) {
	a.buf = append(a.buf[:0], b...)
}

// QueryString returns the encoded representation (no leading '?').
func (a *Args) QueryString() []byte { return a.buf }

// String returns the encoded representation.
func (a *Args) String() string { return string(a.buf) }

// Len returns the number of pairs.
func (a *Args) Len() int { return QueryArgs{raw: a.buf}.Len() }

// Has reports whether key exists.
func (a *Args) Has(key string) bool { return QueryArgs{raw: a.buf}.Has(key) }

// Peek returns the first value for key, or nil.
func (a *Args) Peek(key string) []byte { return QueryArgs{raw: a.buf}.Get(key) }

// GetString returns the first value for key, or "".
func (a *Args) GetString(key string) string { return QueryArgs{raw: a.buf}.GetString(key) }

// Visit calls f for each pair.
func (a *Args) Visit(f func(key, val []byte)) { QueryArgs{raw: a.buf}.Visit(f) }

// Add appends a key/value pair (does not remove existing keys).
func (a *Args) Add(key, value string) {
	if len(a.buf) > 0 {
		a.buf = append(a.buf, '&')
	}
	a.buf = appendQueryEscaped(a.buf, key)
	a.buf = append(a.buf, '=')
	a.buf = appendQueryEscaped(a.buf, value)
}

// Set replaces all values for key with value (or adds if missing).
func (a *Args) Set(key, value string) {
	a.Del(key)
	a.Add(key, value)
}

// Del removes all pairs with the given key.
func (a *Args) Del(key string) {
	if len(a.buf) == 0 || key == "" {
		return
	}
	raw := a.buf
	dst := raw[:0]
	off := 0
	first := true
	for off <= len(raw) {
		end := off
		for end < len(raw) && raw[end] != '&' {
			end++
		}
		pair := raw[off:end]
		eq := bytes.IndexByte(pair, '=')
		var k []byte
		if eq < 0 {
			k = pair
		} else {
			k = pair[:eq]
		}
		keep := true
		if len(k) == len(key) {
			match := true
			for i := 0; i < len(k); i++ {
				if k[i] != key[i] {
					match = false
					break
				}
			}
			if match {
				keep = false
			}
		}
		if keep && len(pair) > 0 {
			if !first {
				dst = append(dst, '&')
			}
			dst = append(dst, pair...)
			first = false
		}
		if end >= len(raw) {
			break
		}
		off = end + 1
	}
	a.buf = dst
}

// CopyTo copies args into dst.
func (a *Args) CopyTo(dst *Args) {
	if dst == nil {
		return
	}
	dst.buf = append(dst.buf[:0], a.buf...)
}

func appendQueryEscaped(dst []byte, s string) []byte {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '-', c == '_', c == '.', c == '~':
			dst = append(dst, c)
		case c == ' ':
			dst = append(dst, '+')
		default:
			dst = append(dst, '%')
			dst = append(dst, "0123456789ABCDEF"[c>>4], "0123456789ABCDEF"[c&0xf])
		}
	}
	return dst
}

// EncodeURLValues copies url.Values into Args (last value wins per Set semantics via Add).
func (a *Args) AddURLValues(v url.Values) {
	for k, vals := range v {
		for _, val := range vals {
			a.Add(k, val)
		}
	}
}

// SetUint sets key to the decimal form of n.
func (a *Args) SetUint(key string, n uint64) {
	var tmp [20]byte
	a.Set(key, string(strconv.AppendUint(tmp[:0], n, 10)))
}
