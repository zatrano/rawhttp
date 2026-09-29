package rawhttp

import (
	"errors"
	"io"
	"sync"
)

// requestStream reads a request body on demand from the connection buffer.
// Used only when Server.StreamRequestBody is true.
type requestStream struct {
	cr *connReader

	contentLength int // >=0 fixed length; -1 chunked
	remaining     int // bytes left for Content-Length body
	totalRead     int
	maxSize       int
	maxTrailers   int
	maxChunks     int

	chunkLeft int
	nChunks   int
	chunked   bool
	done      bool
	err       error
}

var requestStreamPool = sync.Pool{New: func() any { return &requestStream{} }}

func acquireRequestStream(cr *connReader, contentLength, maxSize, maxTrailers, maxChunks int, chunked bool) *requestStream {
	rs := requestStreamPool.Get().(*requestStream)
	*rs = requestStream{
		cr:            cr,
		contentLength: contentLength,
		remaining:     contentLength,
		maxSize:       maxSize,
		maxTrailers:   maxTrailers,
		maxChunks:     maxChunks,
		chunked:       chunked,
	}
	if chunked {
		rs.contentLength = -1
		rs.remaining = -1
	}
	return rs
}

func releaseRequestStream(rs *requestStream) {
	if rs == nil {
		return
	}
	*rs = requestStream{}
	requestStreamPool.Put(rs)
}

func (rs *requestStream) Read(p []byte) (int, error) {
	if rs.err != nil {
		return 0, rs.err
	}
	if rs.done || len(p) == 0 {
		if rs.done {
			return 0, io.EOF
		}
		return 0, nil
	}
	if rs.chunked {
		return rs.readChunked(p)
	}
	return rs.readFixed(p)
}

func (rs *requestStream) readFixed(p []byte) (int, error) {
	if rs.remaining <= 0 {
		rs.done = true
		return 0, io.EOF
	}
	if len(p) > rs.remaining {
		p = p[:rs.remaining]
	}
	n, err := rs.readInto(p)
	rs.remaining -= n
	rs.totalRead += n
	if err != nil {
		rs.err = err
		return n, err
	}
	if rs.remaining == 0 {
		rs.done = true
		return n, io.EOF
	}
	return n, nil
}

func (rs *requestStream) readChunked(p []byte) (int, error) {
	if rs.chunkLeft == 0 {
		line, err := rs.cr.readLine()
		if err != nil {
			rs.err = err
			return 0, err
		}
		size, ok := parseHexSize(line)
		if !ok {
			rs.err = errBadChunk
			return 0, errBadChunk
		}
		if size == 0 {
			if err := rs.readTrailers(); err != nil {
				rs.err = err
				return 0, err
			}
			rs.done = true
			return 0, io.EOF
		}
		rs.nChunks++
		if rs.maxChunks > 0 && rs.nChunks > rs.maxChunks {
			rs.err = ErrBadRequest
			return 0, ErrBadRequest
		}
		if rs.totalRead+size > rs.maxSize {
			rs.err = ErrBodyTooLarge
			return 0, ErrBodyTooLarge
		}
		rs.chunkLeft = size
	}

	want := len(p)
	if want > rs.chunkLeft {
		want = rs.chunkLeft
	}
	n, err := rs.readInto(p[:want])
	rs.chunkLeft -= n
	rs.totalRead += n
	if err != nil {
		rs.err = err
		return n, err
	}
	if rs.chunkLeft == 0 {
		if err := consumeCRLF(rs.cr); err != nil {
			rs.err = err
			return n, err
		}
	}
	return n, nil
}

func (rs *requestStream) readInto(p []byte) (int, error) {
	filled := 0
	for filled < len(p) {
		avail := rs.cr.w - rs.cr.off
		if avail > 0 {
			take := avail
			if take > len(p)-filled {
				take = len(p) - filled
			}
			copy(p[filled:], rs.cr.buf[rs.cr.off:rs.cr.off+take])
			rs.cr.off += take
			filled += take
			continue
		}
		if err := rs.cr.fill(); err != nil {
			if filled > 0 {
				return filled, nil
			}
			return 0, err
		}
	}
	return filled, nil
}

func (rs *requestStream) readTrailers() error {
	nTrail := 0
	maxTrailers := rs.maxTrailers
	if maxTrailers <= 0 {
		maxTrailers = 100
	}
	for {
		t, err := rs.cr.readLine()
		if err != nil {
			return err
		}
		if len(t) == 0 {
			return nil
		}
		if t[0] == ' ' || t[0] == '\t' {
			return ErrBadRequest
		}
		colon := -1
		for i := 0; i < len(t); i++ {
			if t[i] == ':' {
				colon = i
				break
			}
		}
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
		if forbiddenTrailer(key) {
			return ErrBadRequest
		}
		nTrail++
		if nTrail > maxTrailers {
			return ErrBadRequest
		}
	}
}

// drain discards any unread body bytes so the connection can be reused.
func (rs *requestStream) drain() error {
	if rs == nil || rs.done {
		return rs.err
	}
	var buf [8 << 10]byte
	for {
		_, err := rs.Read(buf[:])
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
