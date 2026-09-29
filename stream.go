package rawhttp

import (
	"bufio"
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
func NewStreamReader(sw StreamWriter) io.ReadCloser {
	pr, pw := io.Pipe()
	go func() {
		bw := streamWriterBufPool.Get().(*bufio.Writer)
		bw.Reset(pw)
		sw(bw)
		_ = bw.Flush()
		_ = pw.Close()
		bw.Reset(nil)
		streamWriterBufPool.Put(bw)
	}()
	return pr
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
