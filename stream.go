package rawhttp

import (
	"bufio"
	"errors"
	"io"
	"sync"
)

// StreamWriter writes a streaming response body to w.
// Call w.Flush when data must reach the client promptly.
// Return immediately if w returns an error.
type StreamWriter func(w *bufio.Writer)

var streamWriterBufPool = sync.Pool{New: func() any {
	return bufio.NewWriterSize(nil, 4096)
}}

// NewStreamReader returns a reader that replays data produced by sw.
// Close the reader after use to avoid leaking the producer goroutine.
//
// A panic in sw runs on the producer goroutine, outside the handler's panic
// recovery. It is recovered and ends the stream with ErrStreamPanic, so the
// response is cut short and the connection is closed.
func NewStreamReader(sw StreamWriter) io.ReadCloser {
	pr, pw := io.Pipe()
	go func() {
		bw := streamWriterBufPool.Get().(*bufio.Writer)
		bw.Reset(pw)
		defer func() {
			if rec := recover(); rec != nil {
				_ = pw.CloseWithError(ErrStreamPanic)
				return
			}
			_ = bw.Flush()
			_ = pw.Close()
			bw.Reset(nil)
			streamWriterBufPool.Put(bw)
		}()
		sw(bw)
	}()
	return pr
}

// ErrStreamPanic ends a streamed response whose producer panicked.
var ErrStreamPanic = errors.New("rawhttp: stream body producer panicked")

// readStream reads from a handler-supplied body. A panicking reader would run
// on the connection goroutine after the handler's recovery has ended.
func readStream(r io.Reader, buf []byte) (n int, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			n, err = 0, ErrStreamPanic
		}
	}()
	return r.Read(buf)
}

func readStreamFull(r io.Reader, buf []byte) (n int, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			n, err = 0, ErrStreamPanic
		}
	}()
	return io.ReadFull(r, buf)
}

// SetBodyStreamWriter streams the response body via sw (chunked Transfer-Encoding).
// Do not touch Ctx from inside sw after this returns; write only to w.
func (c *Ctx) SetBodyStreamWriter(sw StreamWriter) {
	c.SetBodyStream(NewStreamReader(sw), -1)
}

// IsBodyStream reports whether the response body was set via SetBodyStream*.
func (c *Ctx) IsBodyStream() bool {
	return c.streamBody != nil
}
