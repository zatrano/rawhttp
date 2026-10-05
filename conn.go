package rawhttp

import (
	"bytes"
	"errors"
	"io"
	"net"
)

type connReader struct {
	conn     net.Conn
	buf      []byte
	r, w     int
	off      int
	pooled   bool
	unpinned bool // release() copied header slices; consumed bytes may be discarded
}

func newConnReader(conn net.Conn, size int) *connReader {
	cr := &connReader{conn: conn}
	if size <= defaultBufSize {
		bp := readerBufPool.Get().(*[]byte)
		b := *bp
		if cap(b) < size {
			b = make([]byte, size)
			cr.pooled = false
		} else {
			b = b[:size]
			cr.pooled = true
		}
		cr.buf = b
	} else {
		cr.buf = make([]byte, size)
	}
	return cr
}

func (cr *connReader) releaseBuf() {
	if !cr.pooled || cr.buf == nil {
		return
	}
	b := cr.buf[:cap(cr.buf)]
	readerBufPool.Put(&b)
	cr.buf = nil
	cr.pooled = false
}

func (cr *connReader) compact() {
	if cr.r == 0 {
		return
	}
	n := copy(cr.buf, cr.buf[cr.r:cr.w])
	cr.w = n
	cr.r = 0
	cr.off = 0
}

func (cr *connReader) fill() error {
	// Prefer reclaiming already-consumed bytes before declaring the buffer full.
	// Compact is only safe when the pin equals the parse cursor (no live slices).
	if cr.r > 0 && cr.r == cr.off && (cr.w == len(cr.buf) || len(cr.buf)-cr.w < 512) {
		cr.compact()
	}
	if cr.w == len(cr.buf) {
		if cr.unpinned && cr.off > cr.r {
			cr.r = cr.off
		}
		if cr.r == cr.off && cr.r > 0 {
			cr.compact()
		} else {
			return errBufferFull
		}
	}
	n, err := cr.conn.Read(cr.buf[cr.w:])
	cr.w += n
	if err != nil {
		return err
	}
	if n == 0 {
		return io.ErrNoProgress
	}
	return nil
}

func (cr *connReader) readLine() ([]byte, error) {
	for {
		if idx := bytes.IndexByte(cr.buf[cr.off:cr.w], '\n'); idx >= 0 {
			start := cr.off
			end := cr.off + idx
			line := cr.buf[start:end]
			cr.off = end + 1
			// Require CRLF (reject bare LF) — smuggling / desync mitigation.
			if len(line) == 0 || line[len(line)-1] != '\r' {
				return nil, ErrBadRequest
			}
			return line[:len(line)-1], nil
		}
		if err := cr.fill(); err != nil {
			return nil, err
		}
	}
}

// takeBufferedBody returns a zero-copy slice of the next n bytes when they
// already sit (or can be filled) in the read buffer without compacting over
// the pinned header region. On success the parse cursor advances by n.
// Returns ok=false when the body cannot fit beside the pinned headers (caller
// must copy headers, release the pin, then readFull).
func (cr *connReader) takeBufferedBody(n int) (body []byte, ok bool) {
	if n <= 0 {
		return nil, true
	}
	for cr.w-cr.off < n {
		if err := cr.fill(); err != nil {
			return nil, false
		}
	}
	body = cr.buf[cr.off : cr.off+n]
	cr.off += n
	return body, true
}

// readFull copies exactly n bytes from the parse cursor into dst, reading
// directly from the connection when the buffer is exhausted — without
// compacting over the pinned header region [r, off).
func (cr *connReader) readFull(dst []byte) error {
	n := len(dst)
	filled := 0
	for filled < n {
		avail := cr.w - cr.off
		if avail > 0 {
			take := avail
			if take > n-filled {
				take = n - filled
			}
			copy(dst[filled:], cr.buf[cr.off:cr.off+take])
			cr.off += take
			filled += take
			continue
		}
		// Buffer empty of unparsed data: read straight into dst.
		nn, err := cr.conn.Read(dst[filled:])
		filled += nn
		if err != nil {
			if filled == n && (errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)) {
				return nil
			}
			return err
		}
		if nn == 0 {
			return io.ErrNoProgress
		}
	}
	return nil
}

func (cr *connReader) release() {
	cr.r = cr.off
	cr.unpinned = true
}
