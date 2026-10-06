package rawhttp

import (
	"net"
	"time"
)

const (
	defaultLingerDrain   = 256 << 10
	defaultLingerTimeout = time.Second
	defaultMaxLingering  = 1024
)

// lingerAfterEarlyError discards leftover request bytes so a client can
// still read the error response. Windows sends RST when Close runs with
// unread data, which drops that response.
//
// The wait is skipped, with no CloseWrite, when the server is shutting
// down or MaxLingering connections are already discarding. The caller
// then closes immediately.
func (s *Server) lingerAfterEarlyError(conn net.Conn) {
	if conn == nil || s == nil || !s.acquireLinger() {
		return
	}
	defer s.Lingering.Add(-1)

	limit := s.LingerDrain
	if limit <= 0 {
		limit = defaultLingerDrain
	}
	timeout := s.LingerTimeout
	if timeout <= 0 {
		timeout = defaultLingerTimeout
	}
	if cw, ok := conn.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
	}
	deadline := time.Now().Add(timeout)
	buf := make([]byte, 16<<10)
	got := 0
	for got < limit {
		left := time.Until(deadline)
		if left <= 0 || s.lingerStopped() {
			return
		}
		_ = conn.SetReadDeadline(time.Now().Add(left))
		if s.lingerStopped() {
			return
		}
		n := limit - got
		if n > len(buf) {
			n = len(buf)
		}
		nr, err := conn.Read(buf[:n])
		got += nr
		if s.lingerStopped() || time.Until(deadline) <= 0 {
			return
		}
		if err != nil || nr == 0 {
			return
		}
	}
	left := time.Until(deadline)
	if left <= 0 || s.lingerStopped() {
		return
	}
	timer := time.NewTimer(left)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-s.lingerStopChan():
	}
}

func (s *Server) lingerCap() int64 {
	if s.MaxLingering < 0 {
		return -1
	}
	if s.MaxLingering == 0 {
		return defaultMaxLingering
	}
	return int64(s.MaxLingering)
}

func (s *Server) acquireLinger() bool {
	if s.shutting.Load() {
		return false
	}
	max := s.lingerCap()
	if max < 0 {
		s.Lingering.Add(1)
		if s.shutting.Load() {
			s.Lingering.Add(-1)
			return false
		}
		return true
	}
	for {
		cur := s.Lingering.Load()
		if cur >= max {
			return false
		}
		if s.Lingering.CompareAndSwap(cur, cur+1) {
			if s.shutting.Load() {
				s.Lingering.Add(-1)
				return false
			}
			return true
		}
	}
}

func (s *Server) lingerStopChan() chan struct{} {
	s.lingerStopOnce.Do(func() {
		s.lingerStop = make(chan struct{})
	})
	return s.lingerStop
}

func (s *Server) lingerStopped() bool {
	if s.shutting.Load() {
		return true
	}
	select {
	case <-s.lingerStopChan():
		return true
	default:
		return false
	}
}

func (s *Server) stopLingerWaits() {
	ch := s.lingerStopChan()
	s.lingerStopMu.Lock()
	select {
	case <-ch:
		s.lingerStopMu.Unlock()
		return
	default:
		close(ch)
	}
	s.lingerStopMu.Unlock()

	// Unblock a discard blocked in Read. Closing the stop channel alone
	// does not interrupt that read.
	s.mu.Lock()
	conns := make([]net.Conn, 0, len(s.conns))
	for cs := range s.conns {
		conns = append(conns, cs.conn)
	}
	s.mu.Unlock()
	now := time.Now()
	for _, c := range conns {
		_ = c.SetReadDeadline(now)
	}
}
