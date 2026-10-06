package rawhttp

import (
	"net"
	"time"
)

const (
	defaultLingerDrain   = 256 << 10
	defaultLingerTimeout = time.Second
)

// lingerAfterEarlyError runs after an early error or RejectStatus response
// has been written with Connection: close. It half-closes when the conn
// supports it, discards a bounded amount of unread request bytes, and
// returns so the caller can Close. It does not start a goroutine.
//
// After the byte cap, the goroutine waits out the remaining timeout before
// returning. Closing sooner aborts the socket on Windows (unread TCP data
// becomes RST) and the client loses the response it has not read yet.
func (s *Server) lingerAfterEarlyError(conn net.Conn) {
	if conn == nil || s == nil {
		return
	}
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
		if left <= 0 {
			return
		}
		_ = conn.SetReadDeadline(time.Now().Add(left))
		n := limit - got
		if n > len(buf) {
			n = len(buf)
		}
		nr, err := conn.Read(buf[:n])
		got += nr
		if err != nil || nr == 0 {
			return
		}
	}
	if left := time.Until(deadline); left > 0 {
		time.Sleep(left)
	}
}
