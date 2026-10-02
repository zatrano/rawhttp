package rawhttp

import (
	"net"
	"time"
)

// Hijack takes ownership of the underlying connection. The server stops
// managing the connection: it will not write the HTTP response. By default the
// accept loop still Closes the conn when the serve goroutine ends; set
// Server.KeepHijackedConns to keep it open for the caller. leftover is any
// unread bytes still buffered (e.g. pipelined data); the caller must process
// them before reading from conn.
//
// Hijack may only be called from within the request Handler.
func (c *Ctx) Hijack() (conn net.Conn, leftover []byte, err error) {
	if c.hijacked {
		return nil, nil, ErrHijacked
	}
	if c.conn == nil {
		return nil, nil, ErrNotHijackable
	}
	if c.cr != nil {
		if n := c.cr.w - c.cr.off; n > 0 {
			leftover = append([]byte(nil), c.cr.buf[c.cr.off:c.cr.w]...)
		}
		c.cr.off = c.cr.w
		c.cr.releaseBuf()
		c.cr = nil
	}
	c.hijacked = true
	conn = c.conn
	c.conn = nil
	c.cacheOK = false
	// Clear server-imposed deadlines; the caller owns timeouts after Hijack.
	_ = conn.SetDeadline(time.Time{})
	return conn, leftover, nil
}

// Hijacked reports whether Hijack was called for this request.
func (c *Ctx) Hijacked() bool { return c.hijacked }

// HijackSetNoResponse controls whether the server would write a response after
// the handler returns when Hijack is used. rawhttp never writes after Hijack;
// this exists for API compatibility.
func (c *Ctx) HijackSetNoResponse(noResponse bool) { c.hijackNoResp = noResponse }

// TimeoutError marks the response as a timeout (503) and ignores further
// writes. Use when retaining Ctx references past handler return (retain-after-handler pattern).
func (c *Ctx) TimeoutError(msg string) {
	if msg == "" {
		msg = "Timeout"
	}
	c.lockResp()
	defer c.unlockResp()
	c.timedOut = true
	c.forceClose = true
	c.respChunked = false
	c.streamBody = nil
	c.streamSize = 0
	c.bodyIsRef = false
	c.cacheOK = false
	c.StatusCode = 503
	c.userSetCT = true
	c.contentType = append(c.contentType[:0], "text/plain; charset=utf-8"...)
	c.respHeaderBuf = c.respHeaderBuf[:0]
	c.respBuf = append(c.respBuf[:0], msg...)
	c.respBody = c.respBuf
}

// TimeoutHandler returns a Handler that runs h with a time limit. If h does
// not finish within timeout, the response becomes status 503 with body msg
// (or "Timeout" when msg is empty) and Connection: close.
//
// The original handler is allowed to finish in the background so pooled Ctx
// state is not reused early; after the timeout, further writes from h are
// ignored (guarded by an internal mutex enabled only for this wrapper).
func TimeoutHandler(h Handler, timeout time.Duration, msg string) Handler {
	if msg == "" {
		msg = "Timeout"
	}
	return func(ctx *Ctx) {
		if timeout <= 0 {
			h(ctx)
			return
		}
		ctx.timeoutGuard = true
		done := make(chan struct{})
		go func() {
			defer close(done)
			defer func() {
				_ = recover() // callHandler also recovers; keep goroutine from crashing process
			}()
			h(ctx)
		}()
		timer := time.NewTimer(timeout)
		select {
		case <-done:
			if !timer.Stop() {
				<-timer.C
			}
		case <-timer.C:
			ctx.TimeoutError(msg)
			<-done
		}
	}
}
