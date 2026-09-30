package rawhttp

import (
	"bytes"
)

var (
	vKeepAlive   = []byte("keep-alive")
	vClose       = []byte("close")
	vChunked     = []byte("chunked")
	v100Continue = []byte("100-continue")
	vUpgrade     = []byte("upgrade")
)

const maxChunkExtLen = 256
const maxChunksPerBody = 16 << 10 // 16384 non-zero chunks
const maxHeaderNameLen = 256

func parseRequestLineOpts(cr *connReader, ctx *Ctx, allowDotDot bool) error {
	line, err := cr.readLine()
	if err != nil {
		return err
	}
	sp1, sp2 := -1, -1
	for i := 0; i < len(line); i++ {
		c := line[i]
		if c == 0 {
			return errMalformedRequestLine
		}
		if c == ' ' {
			if sp1 < 0 {
				sp1 = i
			} else if sp2 < 0 {
				sp2 = i
			} else {
				return errMalformedRequestLine
			}
		}
	}
	if sp1 <= 0 || sp2 < 0 {
		return errMalformedRequestLine
	}

	ctx.Method = line[:sp1]
	if !validMethod(ctx.Method) {
		return errMalformedRequestLine
	}
	m := ctx.Method
	ctx.head = len(m) == 4 && m[0] == 'H' && m[1] == 'E' && m[2] == 'A' && m[3] == 'D'

	target := line[sp1+1 : sp2]
	if len(target) == 0 {
		return errMalformedRequestLine
	}
	// Origin-form ("/path") or asterisk-form ("*") only.
	if target[0] == '*' {
		if len(target) != 1 {
			return ErrBadRequest
		}
		// Asterisk-form is only valid for OPTIONS.
		if len(ctx.Method) != 7 ||
			ctx.Method[0] != 'O' || ctx.Method[1] != 'P' || ctx.Method[2] != 'T' ||
			ctx.Method[3] != 'I' || ctx.Method[4] != 'O' || ctx.Method[5] != 'N' || ctx.Method[6] != 'S' {
			return ErrBadRequest
		}
		ctx.Path = target
		ctx.Query = nil
		// fall through to version parse
	} else if target[0] != '/' {
		return ErrBadRequest
	} else {
		// Reject scheme-relative "//host/..." (open-redirect / authority confusion).
		if len(target) >= 2 && target[1] == '/' {
			return ErrBadRequest
		}
		if len(target) > 1 && !validRequestTargetAllow(target, allowDotDot) {
			return ErrBadRequest
		}
		if q := bytes.IndexByte(target, '?'); q >= 0 {
			ctx.Path = target[:q]
			ctx.Query = target[q+1:]
		} else {
			ctx.Path = target
			ctx.Query = nil
		}
	}
	version := line[sp2+1:]
	// Only HTTP/1.0 and HTTP/1.1.
	if len(version) != 8 ||
		version[0] != 'H' || version[1] != 'T' || version[2] != 'T' || version[3] != 'P' ||
		version[4] != '/' || version[5] != '1' || version[6] != '.' ||
		(version[7] != '0' && version[7] != '1') {
		return errMalformedRequestLine
	}
	if version[7] == '0' {
		ctx.httpMinor = 0
	} else {
		ctx.httpMinor = 1
	}
	return nil
}

func isMethodToken(m []byte) bool {
	if len(m) == 0 {
		return false
	}
	for _, c := range m {
		switch {
		case c >= 'A' && c <= 'Z':
		case c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9':
		case c == '!' || c == '#' || c == '$' || c == '%' || c == '&' || c == '\'' ||
			c == '*' || c == '+' || c == '-' || c == '.' || c == '^' || c == '_' ||
			c == '`' || c == '|' || c == '~':
		default:
			return false
		}
	}
	return true
}

// validMethod fast-paths the common methods used on the plaintext hot path.
func validMethod(m []byte) bool {
	switch len(m) {
	case 3:
		if m[0] == 'G' && m[1] == 'E' && m[2] == 'T' {
			return true
		}
		if m[0] == 'P' && m[1] == 'U' && m[2] == 'T' {
			return true
		}
	case 4:
		if m[0] == 'H' && m[1] == 'E' && m[2] == 'A' && m[3] == 'D' {
			return true
		}
		if m[0] == 'P' && m[1] == 'O' && m[2] == 'S' && m[3] == 'T' {
			return true
		}
	case 5:
		if m[0] == 'P' && m[1] == 'A' && m[2] == 'T' && m[3] == 'C' && m[4] == 'H' {
			return true
		}
	case 6:
		if m[0] == 'D' && m[1] == 'E' && m[2] == 'L' && m[3] == 'E' && m[4] == 'T' && m[5] == 'E' {
			return true
		}
	case 7:
		if m[0] == 'O' && m[1] == 'P' && m[2] == 'T' && m[3] == 'I' && m[4] == 'O' && m[5] == 'N' && m[6] == 'S' {
			return true
		}
	}
	if forbiddenMethod(m) {
		return false
	}
	return len(m) <= maxMethodLen && isMethodToken(m)
}

// forbiddenMethod rejects TRACE/CONNECT (any case) — unsupported and risky.
func forbiddenMethod(m []byte) bool {
	switch len(m) {
	case 5: // TRACE
		return (m[0]|0x20) == 't' && (m[1]|0x20) == 'r' && (m[2]|0x20) == 'a' &&
			(m[3]|0x20) == 'c' && (m[4]|0x20) == 'e'
	case 7: // CONNECT
		return (m[0]|0x20) == 'c' && (m[1]|0x20) == 'o' && (m[2]|0x20) == 'n' &&
			(m[3]|0x20) == 'n' && (m[4]|0x20) == 'e' && (m[5]|0x20) == 'c' && (m[6]|0x20) == 't'
	}
	return false
}

func noBodyMethod(m []byte) bool {
	switch len(m) {
	case 3:
		return m[0] == 'G' && m[1] == 'E' && m[2] == 'T'
	case 4:
		return m[0] == 'H' && m[1] == 'E' && m[2] == 'A' && m[3] == 'D'
	default:
		return false
	}
}

func isGetMethod(m []byte) bool {
	return len(m) == 3 && m[0] == 'G' && m[1] == 'E' && m[2] == 'T'
}

// validRequestTarget rejects CTL, backslash, fragment '#', %00, encoded dots (%2e),
// and ".." segments.
func validRequestTargetAllow(t []byte, allowDotDot bool) bool {
	for i := 0; i < len(t); i++ {
		c := t[i]
		if c < 0x20 || c == 0x7f || c == '\\' || c == '#' {
			return false
		}
		if c == '%' && i+2 < len(t) {
			h1, h2 := t[i+1]|0x20, t[i+2]|0x20
			if h1 == '0' && h2 == '0' {
				return false
			}
			// Reject %2e / %2E so proxies that decode cannot form ".." segments.
			if !allowDotDot && h1 == '2' && h2 == 'e' {
				return false
			}
		}
	}
	// Hot path: skip segment scan unless ".." appears.
	if !allowDotDot && bytes.Contains(t, []byte("..")) && hasDotDotSegment(t) {
		return false
	}
	return true
}

// hasDotDotSegment reports path traversal segments ("..", "/../", prefix "../", suffix "/..").
func hasDotDotSegment(path []byte) bool {
	n := len(path)
	if n == 0 {
		return false
	}
	if n == 2 && path[0] == '.' && path[1] == '.' {
		return true
	}
	if n >= 3 && path[0] == '.' && path[1] == '.' && path[2] == '/' {
		return true
	}
	if n >= 3 && path[n-3] == '/' && path[n-2] == '.' && path[n-1] == '.' {
		return true
	}
	return bytes.Contains(path, []byte("/../"))
}

func parseHeaders(cr *connReader, ctx *Ctx, maxHeaders, maxHdrBytes int, allowUpgrade bool) error {
	start := cr.off
	nHdr := 0
	for {
		line, err := cr.readLine()
		if err != nil {
			return err
		}
		if maxHdrBytes > 0 && cr.off-start > maxHdrBytes {
			return errBufferFull
		}
		if len(line) == 0 {
			ctx.headerBlock = cr.buf[start:cr.off]
			if ctx.httpMinor >= 1 && !ctx.sawHost {
				return errMissingHost
			}
			if ctx.chunked && ctx.clSet {
				return errChunkedConflict
			}
			// GET/HEAD must not carry a request body.
			if noBodyMethod(ctx.Method) && (ctx.chunked || (ctx.clSet && ctx.contentLength > 0)) {
				return ErrBadRequest
			}
			// Expect: 100-continue is meaningless without a body.
			if ctx.expectContinue && noBodyMethod(ctx.Method) {
				return ErrBadRequest
			}
			return nil
		}
		// Reject obs-fold (line starting with SP/HTAB).
		if line[0] == ' ' || line[0] == '\t' {
			return ErrBadRequest
		}
		nHdr++
		if nHdr > maxHeaders {
			return ErrBadRequest
		}
		colon := bytes.IndexByte(line, ':')
		if colon <= 0 {
			return ErrBadRequest
		}
		if bytes.IndexByte(line, 0) >= 0 {
			return ErrBadRequest
		}
		key := line[:colon]
		if len(key) > maxHeaderNameLen || !isHeaderNameToken(key) {
			return ErrBadRequest
		}
		val := line[colon+1:]
		for len(val) > 0 && (val[0] == ' ' || val[0] == '\t') {
			val = val[1:]
		}
		for len(val) > 0 {
			c := val[len(val)-1]
			if c != ' ' && c != '\t' {
				break
			}
			val = val[:len(val)-1]
		}
		if !validHeaderValue(val) {
			return ErrBadRequest
		}

		known := false
		switch key[0] | 0x20 {
		case 'h':
			if len(key) == 4 &&
				(key[1]|0x20) == 'o' && (key[2]|0x20) == 's' && (key[3]|0x20) == 't' {
				if len(val) == 0 || !validHostValue(val) {
					return errMissingHost
				}
				if ctx.sawHost {
					return errDuplicateHost
				}
				ctx.host = val
				ctx.sawHost = true
				known = true
			}
		case 'a':
			if len(key) == 15 && isAcceptEncoding(key) {
				ctx.acceptEncoding = val
				known = true
			} else if len(key) == 13 && isAuthorization(key) {
				ctx.authorization = val
				known = true
			} else if len(key) == 6 && isAccept(key) {
				ctx.accept = val
				known = true
			}
		case 'o':
			if len(key) == 6 && isOrigin(key) {
				ctx.origin = val
				known = true
			}
		case 'c':
			if len(key) == 14 && isContentLength(key) {
				n, ok := parseContentLength(val)
				if !ok {
					return errInvalidContentLength
				}
				if ctx.clSet {
					return errDuplicateCL
				}
				ctx.contentLength = n
				ctx.clSet = true
				known = true
			} else if len(key) == 10 && isConnection(key) {
				closeH, keepH, upH := parseConnectionDirectives(val)
				if upH && !allowUpgrade {
					return ErrBadRequest
				}
				// Sticky: a later Connection without "upgrade" must not clear
				// an earlier upgrade token (AllowUpgrade admission depends on it).
				ctx.upgradeWanted = ctx.upgradeWanted || upH
				ctx.keepAliveHeader = ctx.keepAliveHeader || keepH
				ctx.closeHeader = ctx.closeHeader || closeH
				known = true
			} else if len(key) == 12 && isContentType(key) {
				ctx.reqContentType = val
				known = true
			} else if len(key) == 6 && isCookie(key) {
				ctx.cookieHdr = val
				known = true
			}
		case 'p':
			if len(key) == 16 && isProxyConnection(key) {
				return ErrBadRequest
			}
		case 't':
			if len(key) == 2 && (key[1]|0x20) == 'e' {
				// TE hop-by-hop; unsupported (no trailer negotiation).
				return ErrBadRequest
			}
			if len(key) == 17 && isTransferEncoding(key) {
				if ctx.chunked || !equalFoldBytes(val, vChunked) {
					return ErrBadRequest
				}
				ctx.chunked = true
				known = true
			}
		case 'e':
			if len(key) == 6 && isExpect(key) {
				if hasToken(val, v100Continue) {
					ctx.expectContinue = true
					known = true
				} else {
					return errExpectationFailed
				}
			}
		case 'u':
			if len(key) == 7 && isUpgrade(key) {
				if !allowUpgrade {
					return ErrBadRequest
				}
				ctx.upgradeProto = val
				known = true
			}
			if len(key) == 10 && isUserAgent(key) {
				ctx.userAgent = val
				known = true
			}
		case 'r':
			if len(key) == 7 && isReferer(key) {
				ctx.referer = val
				known = true
			}
		}
		if !known {
			ctx.extraKeys = append(ctx.extraKeys, key)
			ctx.extraVals = append(ctx.extraVals, val)
		}
	}
}

func isContentType(key []byte) bool {
	// Content-Type (len 12); key[0] already matched 'c'/'C'.
	return (key[1]|0x20) == 'o' && (key[2]|0x20) == 'n' && (key[3]|0x20) == 't' &&
		(key[4]|0x20) == 'e' && (key[5]|0x20) == 'n' && (key[6]|0x20) == 't' && key[7] == '-' &&
		(key[8]|0x20) == 't' && (key[9]|0x20) == 'y' && (key[10]|0x20) == 'p' && (key[11]|0x20) == 'e'
}

func isUserAgent(key []byte) bool {
	// User-Agent (len 10); key[0] already matched 'u'/'U'.
	return (key[1]|0x20) == 's' && (key[2]|0x20) == 'e' && (key[3]|0x20) == 'r' && key[4] == '-' &&
		(key[5]|0x20) == 'a' && (key[6]|0x20) == 'g' && (key[7]|0x20) == 'e' &&
		(key[8]|0x20) == 'n' && (key[9]|0x20) == 't'
}

func isReferer(key []byte) bool {
	// Referer (len 7); key[0] already matched 'r'/'R'.
	return (key[1]|0x20) == 'e' && (key[2]|0x20) == 'f' && (key[3]|0x20) == 'e' &&
		(key[4]|0x20) == 'r' && (key[5]|0x20) == 'e' && (key[6]|0x20) == 'r'
}

func isAccept(key []byte) bool {
	// Accept (len 6); key[0] already matched 'a'/'A'.
	return (key[1]|0x20) == 'c' && (key[2]|0x20) == 'c' && (key[3]|0x20) == 'e' &&
		(key[4]|0x20) == 'p' && (key[5]|0x20) == 't'
}

func isAuthorization(key []byte) bool {
	// Authorization (len 13); key[0] already matched 'a'/'A'.
	return (key[1]|0x20) == 'u' && (key[2]|0x20) == 't' && (key[3]|0x20) == 'h' &&
		(key[4]|0x20) == 'o' && (key[5]|0x20) == 'r' && (key[6]|0x20) == 'i' &&
		(key[7]|0x20) == 'z' && (key[8]|0x20) == 'a' && (key[9]|0x20) == 't' &&
		(key[10]|0x20) == 'i' && (key[11]|0x20) == 'o' && (key[12]|0x20) == 'n'
}

func isOrigin(key []byte) bool {
	// Origin (len 6); key[0] already matched 'o'/'O'.
	return (key[1]|0x20) == 'r' && (key[2]|0x20) == 'i' && (key[3]|0x20) == 'g' &&
		(key[4]|0x20) == 'i' && (key[5]|0x20) == 'n'
}

func isAcceptEncoding(key []byte) bool {
	// Accept-Encoding (len 15); key[0] already matched 'a'/'A'.
	return (key[1]|0x20) == 'c' && (key[2]|0x20) == 'c' && (key[3]|0x20) == 'e' &&
		(key[4]|0x20) == 'p' && (key[5]|0x20) == 't' && key[6] == '-' &&
		(key[7]|0x20) == 'e' && (key[8]|0x20) == 'n' && (key[9]|0x20) == 'c' &&
		(key[10]|0x20) == 'o' && (key[11]|0x20) == 'd' && (key[12]|0x20) == 'i' &&
		(key[13]|0x20) == 'n' && (key[14]|0x20) == 'g'
}

func isCookie(key []byte) bool {
	// Cookie (len 6); key[0] already matched 'c'/'C'.
	return (key[1]|0x20) == 'o' && (key[2]|0x20) == 'o' && (key[3]|0x20) == 'k' &&
		(key[4]|0x20) == 'i' && (key[5]|0x20) == 'e'
}

func isContentLength(key []byte) bool {
	return len(key) == 14 &&
		(key[1]|0x20) == 'o' && (key[2]|0x20) == 'n' && (key[3]|0x20) == 't' &&
		(key[4]|0x20) == 'e' && (key[5]|0x20) == 'n' && (key[6]|0x20) == 't' && key[7] == '-' &&
		(key[8]|0x20) == 'l' && (key[9]|0x20) == 'e' && (key[10]|0x20) == 'n' &&
		(key[11]|0x20) == 'g' && (key[12]|0x20) == 't' && (key[13]|0x20) == 'h'
}

func isConnection(key []byte) bool {
	return len(key) == 10 &&
		(key[1]|0x20) == 'o' && (key[2]|0x20) == 'n' && (key[3]|0x20) == 'n' &&
		(key[4]|0x20) == 'e' && (key[5]|0x20) == 'c' && (key[6]|0x20) == 't' &&
		(key[7]|0x20) == 'i' && (key[8]|0x20) == 'o' && (key[9]|0x20) == 'n'
}

func isTransferEncoding(key []byte) bool {
	return len(key) == 17 &&
		(key[1]|0x20) == 'r' && (key[2]|0x20) == 'a' && (key[3]|0x20) == 'n' &&
		(key[4]|0x20) == 's' && (key[5]|0x20) == 'f' && (key[6]|0x20) == 'e' &&
		(key[7]|0x20) == 'r' && key[8] == '-' && (key[9]|0x20) == 'e' &&
		(key[10]|0x20) == 'n' && (key[11]|0x20) == 'c' && (key[12]|0x20) == 'o' &&
		(key[13]|0x20) == 'd' && (key[14]|0x20) == 'i' && (key[15]|0x20) == 'n' &&
		(key[16]|0x20) == 'g'
}

func isExpect(key []byte) bool {
	return (key[1]|0x20) == 'x' && (key[2]|0x20) == 'p' && (key[3]|0x20) == 'e' &&
		(key[4]|0x20) == 'c' && (key[5]|0x20) == 't'
}

func isUpgrade(key []byte) bool {
	return (key[1]|0x20) == 'p' && (key[2]|0x20) == 'g' && (key[3]|0x20) == 'r' &&
		(key[4]|0x20) == 'a' && (key[5]|0x20) == 'd' && (key[6]|0x20) == 'e'
}

func isProxyConnection(key []byte) bool {
	// Proxy-Connection (len 16); key[0] already matched 'p'/'P'.
	return (key[1]|0x20) == 'r' && (key[2]|0x20) == 'o' &&
		(key[3]|0x20) == 'x' && (key[4]|0x20) == 'y' && key[5] == '-' &&
		(key[6]|0x20) == 'c' && (key[7]|0x20) == 'o' && (key[8]|0x20) == 'n' &&
		(key[9]|0x20) == 'n' && (key[10]|0x20) == 'e' && (key[11]|0x20) == 'c' &&
		(key[12]|0x20) == 't' && (key[13]|0x20) == 'i' && (key[14]|0x20) == 'o' &&
		(key[15]|0x20) == 'n'
}

func isHeaderNameToken(key []byte) bool {
	if len(key) == 0 {
		return false
	}
	// Hot path: letters, digits, hyphen only (covers Host/Connection/CL/TE/…).
	for _, c := range key {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' {
			continue
		}
		return isMethodToken(key)
	}
	return true
}

func validHostValue(v []byte) bool {
	if len(v) == 0 || len(v) > 255 {
		return false
	}
	for _, c := range v {
		// ASCII-only Host: reject high bytes (proxy desync / IDN footguns).
		if c >= 0x80 {
			return false
		}
		if c <= 0x20 || c == 0x7f || c == '@' || c == '/' || c == '\\' || c == ',' {
			return false
		}
	}
	return true
}

func validHeaderValue(v []byte) bool {
	for _, c := range v {
		if c == '\t' {
			continue
		}
		if c < 0x20 || c == 0x7f {
			return false
		}
	}
	return true
}

func parseContentLength(b []byte) (int, bool) {
	if len(b) == 0 {
		return 0, false
	}
	// Reject leading zeros (except "0") — common smuggling footgun.
	if len(b) > 1 && b[0] == '0' {
		return 0, false
	}
	n := 0
	for _, c := range b {
		if c < '0' || c > '9' {
			return 0, false
		}
		d := int(c - '0')
		if n > (int(^uint(0)>>1)-d)/10 {
			return 0, false
		}
		n = n*10 + d
	}
	return n, true
}

func equalFoldBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		x, y := a[i], b[i]
		if x == y {
			continue
		}
		if x >= 'A' && x <= 'Z' {
			x += 'a' - 'A'
		}
		if y >= 'A' && y <= 'Z' {
			y += 'a' - 'A'
		}
		if x != y {
			return false
		}
	}
	return true
}

func hasToken(val, token []byte) bool {
	for i := 0; i+len(token) <= len(val); i++ {
		ok := true
		for j := 0; j < len(token); j++ {
			c := val[i+j]
			if c >= 'A' && c <= 'Z' {
				c += 'a' - 'A'
			}
			if c != token[j] {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		if i > 0 {
			prev := val[i-1]
			if prev != ',' && prev != ' ' && prev != '\t' {
				continue
			}
		}
		if i+len(token) < len(val) {
			next := val[i+len(token)]
			if next != ',' && next != ' ' && next != '\t' {
				continue
			}
		}
		return true
	}
	return false
}

// parseConnectionDirectives scans a Connection header value once.
func parseConnectionDirectives(val []byte) (closeHeader, keepAlive, upgrade bool) {
	i := 0
	n := len(val)
	for i < n {
		for i < n && (val[i] == ' ' || val[i] == '\t' || val[i] == ',') {
			i++
		}
		if i >= n {
			break
		}
		start := i
		for i < n && val[i] != ',' && val[i] != ' ' && val[i] != '\t' {
			i++
		}
		tok := val[start:i]
		switch {
		case equalFoldBytes(tok, vClose):
			closeHeader = true
		case equalFoldBytes(tok, vKeepAlive):
			keepAlive = true
		case equalFoldBytes(tok, vUpgrade):
			upgrade = true
		}
	}
	return
}

func readRequestBody(cr *connReader, ctx *Ctx, maxSize, maxTrailers, maxChunks int) error {
	if ctx.chunked {
		return readChunkedBody(cr, ctx, maxSize, maxTrailers, maxChunks)
	}
	if !ctx.clSet || ctx.contentLength <= 0 {
		return nil
	}
	n := ctx.contentLength
	if n > maxSize {
		return ErrBodyTooLarge
	}
	buf := ctx.reqBody
	if cap(buf) < n {
		buf = make([]byte, n)
	} else {
		buf = buf[:n]
	}
	if err := cr.readFull(buf); err != nil {
		return err
	}
	ctx.reqBody = buf
	return nil
}

// prepareRequestBodyStream attaches a streaming body reader instead of buffering.
// Content-Length over maxSize is rejected immediately (413).
func prepareRequestBodyStream(cr *connReader, ctx *Ctx, maxSize, maxTrailers, maxChunks int) error {
	if ctx.chunked {
		ctx.reqStream = acquireRequestStream(cr, -1, maxSize, maxTrailers, maxChunks, true)
		return nil
	}
	if !ctx.clSet || ctx.contentLength <= 0 {
		return nil
	}
	if ctx.contentLength > maxSize {
		return ErrBodyTooLarge
	}
	ctx.reqStream = acquireRequestStream(cr, ctx.contentLength, maxSize, maxTrailers, maxChunks, false)
	return nil
}
