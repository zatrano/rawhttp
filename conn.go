package rawhttp

import (
	"bytes"
	"errors"
	"io"
	"net"
	"sync"
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
	cr.buf, cr.pooled = acquireReadBuf(size)
	return cr
}

func (cr *connReader) releaseBuf() {
	if cr.buf == nil {
		return
	}
	recycleReadBuf(cr.buf)
	cr.buf = nil
	cr.pooled = false
}

// shrinkIdle drops a grown header buffer once the connection has no unread
// bytes. The next request starts from the small buffer again.
func (cr *connReader) shrinkIdle(want int) {
	if cr.buf == nil || cr.w != cr.off || cap(cr.buf) <= want {
		return
	}
	old := cr.buf
	cr.buf, cr.pooled = acquireReadBuf(want)
	cr.r, cr.w, cr.off = 0, 0, 0
	cr.unpinned = false
	recycleReadBuf(old)
}

// fillHeaders reads until the header block is complete, the header ceiling is
// reached, or the socket fails. The buffer grows 4 KiB → 8 KiB → 16 KiB, then
// by doubling up to limit. A body is not a reason to grow: this returns as
// soon as \r\n\r\n is present.
func (cr *connReader) fillHeaders(limit int) error {
	if limit <= 0 {
		limit = defaultMaxHeaderBytes
	}
	for {
		if headerBlockReady(cr.buf[cr.off:cr.w]) {
			return nil
		}
		if cr.w-cr.off >= limit {
			return nil
		}
		if cr.w < len(cr.buf) {
			if err := cr.fill(); err != nil {
				// A short read still has to reach the parser so a bare LF
				// or a truncated request line becomes 400, not a silent close.
				if cr.w > cr.off && (errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)) {
					return nil
				}
				return err
			}
			continue
		}
		if len(cr.buf) >= limit || !cr.grow(limit) {
			return nil
		}
	}
}

func (cr *connReader) grow(limit int) bool {
	cur := len(cr.buf)
	if cur >= limit {
		return false
	}
	next := readBufInitial
	if cur >= readBufInitial {
		next = cur * 2
	}
	if next > limit {
		next = limit
	}
	if next <= cur {
		return false
	}
	nb, pooled := acquireReadBuf(next)
	n := copy(nb, cr.buf[cr.r:cr.w])
	recycleReadBuf(cr.buf)
	cr.buf = nb
	cr.pooled = pooled
	cr.off -= cr.r
	if cr.off < 0 {
		cr.off = 0
	}
	cr.w = n
	cr.r = 0
	return true
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

// grownBufPool bounds how many enlarged read buffers stay alive between
// connections. Buffers past the cap are left for the garbage collector.
type grownBufPool struct {
	mu   sync.Mutex
	n    int
	bufs [][]byte
}

func (p *grownBufPool) get(size int) []byte {
	p.mu.Lock()
	if p.n > 0 {
		p.n--
		b := p.bufs[p.n]
		p.bufs[p.n] = nil
		p.mu.Unlock()
		if cap(b) >= size {
			return b[:size]
		}
	} else {
		p.mu.Unlock()
	}
	return make([]byte, size)
}

func (p *grownBufPool) put(b []byte) {
	if cap(b) == 0 {
		return
	}
	p.mu.Lock()
	if p.n >= grownReadPoolMax {
		p.mu.Unlock()
		return
	}
	if p.bufs == nil {
		p.bufs = make([][]byte, grownReadPoolMax)
	}
	p.bufs[p.n] = b[:cap(b)]
	p.n++
	p.mu.Unlock()
}

var (
	readerBuf4K = sync.Pool{New: func() any {
		b := make([]byte, readBufInitial)
		return &b
	}}
	grown8  grownBufPool
	grown16 grownBufPool
)

func acquireReadBuf(size int) ([]byte, bool) {
	if size <= 0 {
		size = readBufInitial
	}
	switch {
	case size <= readBufInitial:
		bp := readerBuf4K.Get().(*[]byte)
		b := *bp
		if cap(b) != readBufInitial {
			return make([]byte, size), false
		}
		return b[:size], true
	case size <= readBufStep8:
		return grown8.get(size), capClass(size)
	case size <= readBufStep16:
		return grown16.get(size), capClass(size)
	default:
		return make([]byte, size), false
	}
}

func capClass(size int) bool {
	return size == readBufStep8 || size == readBufStep16 || size == readBufInitial
}

func recycleReadBuf(b []byte) {
	if b == nil {
		return
	}
	switch cap(b) {
	case readBufInitial:
		b = b[:readBufInitial]
		readerBuf4K.Put(&b)
	case readBufStep8:
		grown8.put(b[:readBufStep8])
	case readBufStep16:
		grown16.put(b[:readBufStep16])
	default:
		if cap(b) <= defaultBufSize && cap(b) > readBufInitial {
			b = b[:cap(b)]
			readerBufPool.Put(&b)
		}
	}
}
