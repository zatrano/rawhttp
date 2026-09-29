package rawhttp

import (
	"bytes"
	"strconv"
)

func readChunkedBody(cr *connReader, ctx *Ctx, maxSize, maxTrailers, maxChunks int) error {
	buf := ctx.reqBody[:0]
	nChunks := 0
	if maxChunks <= 0 {
		maxChunks = maxChunksPerBody
	}
	for {
		line, err := cr.readLine()
		if err != nil {
			return err
		}
		size, ok := parseHexSize(line)
		if !ok {
			return errBadChunk
		}
		if size == 0 {
			nTrail := 0
			for {
				t, err := cr.readLine()
				if err != nil {
					return err
				}
				if len(t) == 0 {
					break
				}
				if t[0] == ' ' || t[0] == '\t' {
					return ErrBadRequest
				}
				colon := bytes.IndexByte(t, ':')
				if colon <= 0 {
					return ErrBadRequest
				}
				key := t[:colon]
				if len(key) > maxHeaderNameLen || !isHeaderNameToken(key) {
					return ErrBadRequest
				}
				val := t[colon+1:]
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
				// Smuggling-sensitive headers must not appear as trailers.
				if forbiddenTrailer(key) {
					return ErrBadRequest
				}
				nTrail++
				if nTrail > maxTrailers {
					return ErrBadRequest
				}
			}
			ctx.reqBody = buf
			ctx.contentLength = len(buf)
			return nil
		}
		nChunks++
		if nChunks > maxChunks {
			return ErrBadRequest
		}
		if len(buf)+size > maxSize {
			return ErrBodyTooLarge
		}
		need := size
		for need > 0 {
			avail := cr.w - cr.off
			if avail == 0 {
				if err := cr.fill(); err != nil {
					return err
				}
				avail = cr.w - cr.off
			}
			take := avail
			if take > need {
				take = need
			}
			buf = append(buf, cr.buf[cr.off:cr.off+take]...)
			cr.off += take
			need -= take
		}
		if err := consumeCRLF(cr); err != nil {
			return err
		}
	}
}

func appendChunk(dst, p []byte) []byte {
	dst = strconv.AppendUint(dst, uint64(len(p)), 16)
	dst = append(dst, '\r', '\n')
	dst = append(dst, p...)
	dst = append(dst, '\r', '\n')
	return dst
}

func appendChunkString(dst []byte, s string) []byte {
	dst = strconv.AppendUint(dst, uint64(len(s)), 16)
	dst = append(dst, '\r', '\n')
	dst = append(dst, s...)
	dst = append(dst, '\r', '\n')
	return dst
}

func forbiddenTrailer(key []byte) bool {
	switch len(key) {
	case 4: // Host
		return (key[0]|0x20) == 'h' && (key[1]|0x20) == 'o' && (key[2]|0x20) == 's' && (key[3]|0x20) == 't'
	case 7: // Trailer / Upgrade
		k0 := key[0] | 0x20
		if k0 == 't' {
			return (key[1]|0x20) == 'r' && (key[2]|0x20) == 'a' && (key[3]|0x20) == 'i' &&
				(key[4]|0x20) == 'l' && (key[5]|0x20) == 'e' && (key[6]|0x20) == 'r'
		}
		if k0 == 'u' {
			return isUpgrade(key)
		}
	case 10: // Connection
		return (key[0]|0x20) == 'c' && isConnection(key)
	case 14: // Content-Length
		return (key[0]|0x20) == 'c' && isContentLength(key)
	case 17: // Transfer-Encoding
		return (key[0]|0x20) == 't' && isTransferEncoding(key)
	}
	return false
}

func parseHexSize(line []byte) (int, bool) {
	i := 0
	for i < len(line) {
		c := line[i]
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') {
			i++
			continue
		}
		break
	}
	if i == 0 || i > 16 {
		return 0, false
	}
	hexPart := line[:i]
	rest := line[i:]
	for len(rest) > 0 && (rest[0] == ' ' || rest[0] == '\t') {
		rest = rest[1:]
	}
	if len(rest) > 0 {
		if rest[0] != ';' {
			return 0, false
		}
		ext := rest[1:]
		if len(ext)+1 > maxChunkExtLen { // include ';'
			return 0, false
		}
		for _, ec := range ext {
			if ec < 0x20 || ec == 0x7f {
				return 0, false
			}
		}
	}
	n := 0
	for _, c := range hexPart {
		var d int
		switch {
		case c >= '0' && c <= '9':
			d = int(c - '0')
		case c >= 'a' && c <= 'f':
			d = int(c-'a') + 10
		case c >= 'A' && c <= 'F':
			d = int(c-'A') + 10
		default:
			return 0, false
		}
		if n > (int(^uint(0)>>1)-d)/16 {
			return 0, false
		}
		n = n*16 + d
	}
	return n, true
}

func consumeCRLF(cr *connReader) error {
	var b [2]byte
	if err := cr.readFull(b[:]); err != nil {
		return err
	}
	if b[0] != '\r' || b[1] != '\n' {
		return errBadChunk
	}
	return nil
}
