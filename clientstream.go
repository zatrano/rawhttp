package rawhttp

import (
	"errors"
	"io"
)

// ownedBodyStream wraps a requestStream and returns the clientConn to the pool on Close.
type ownedBodyStream struct {
	rs        *requestStream
	hc        *HostClient
	cc        *clientConn
	closeConn bool
	closed    bool
}

func (o *ownedBodyStream) Read(p []byte) (int, error) {
	if o == nil || o.rs == nil {
		return 0, io.EOF
	}
	return o.rs.Read(p)
}

func (o *ownedBodyStream) Close() error {
	if o == nil || o.closed {
		return nil
	}
	o.closed = true
	if o.rs != nil {
		_ = o.rs.drain()
		releaseRequestStream(o.rs)
		o.rs = nil
	}
	if o.hc != nil && o.cc != nil {
		if o.closeConn {
			o.hc.closeConn(o.cc)
		} else {
			o.hc.release(o.cc)
		}
		o.hc = nil
		o.cc = nil
	}
	return nil
}

// eofBodyStream reads until EOF (no Content-Length / not chunked). Always closes the conn.
type eofBodyStream struct {
	cr      *connReader
	maxSize int
	total   int
	hc      *HostClient
	cc      *clientConn
	err     error
	closed  bool
	done    bool
}

func (s *eofBodyStream) Read(p []byte) (int, error) {
	if s.err != nil {
		return 0, s.err
	}
	if s.done || len(p) == 0 {
		if s.done {
			return 0, io.EOF
		}
		return 0, nil
	}
	avail := s.cr.w - s.cr.off
	if avail == 0 {
		if err := s.cr.fill(); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				s.done = true
				return 0, io.EOF
			}
			s.err = err
			return 0, err
		}
		avail = s.cr.w - s.cr.off
	}
	take := avail
	if take > len(p) {
		take = len(p)
	}
	if s.maxSize > 0 && s.total+take > s.maxSize {
		s.err = ErrBodyTooLarge
		return 0, ErrBodyTooLarge
	}
	n := copy(p, s.cr.buf[s.cr.off:s.cr.off+take])
	s.cr.off += n
	s.total += n
	return n, nil
}

func (s *eofBodyStream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	var buf [8 << 10]byte
	for !s.done && s.err == nil {
		_, err := s.Read(buf[:])
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			break
		}
	}
	if s.hc != nil && s.cc != nil {
		s.hc.closeConn(s.cc)
		s.hc = nil
		s.cc = nil
	}
	return nil
}
