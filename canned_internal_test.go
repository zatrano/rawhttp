package rawhttp

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

// cannedSink is a write-only net.Conn over a buffer.
type cannedSink struct{ w *bytes.Buffer }

func (c *cannedSink) Read([]byte) (int, error)         { return 0, io.EOF }
func (c *cannedSink) Write(b []byte) (int, error)      { return c.w.Write(b) }
func (c *cannedSink) Close() error                     { return nil }
func (c *cannedSink) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (c *cannedSink) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (c *cannedSink) SetDeadline(time.Time) error      { return nil }
func (c *cannedSink) SetReadDeadline(time.Time) error  { return nil }
func (c *cannedSink) SetWriteDeadline(time.Time) error { return nil }

// Every canned error response must be framed exactly: a client that trusts
// Content-Length must end at the last body byte.
func TestCannedResponsesAreWellFramed(t *testing.T) {
	canned := map[string][]byte{
		"400": status400, "408": status408, "413": status413, "414": status414, "417": status417,
		"421": status421, "429": status429, "431": status431,
	}
	for name, raw := range canned {
		br := bufio.NewReader(bytes.NewReader(raw))
		resp, err := http.ReadResponse(br, nil)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("%s body: %v", name, err)
		}
		if int64(len(body)) != resp.ContentLength {
			t.Fatalf("%s: body %d bytes, Content-Length %d", name, len(body), resp.ContentLength)
		}
		if rest, _ := io.ReadAll(br); len(rest) != 0 {
			t.Fatalf("%s: %d bytes after the declared body: %q", name, len(rest), rest)
		}
		if !resp.Close {
			t.Fatalf("%s: canned error response does not close", name)
		}
	}
}

func TestFixedStatusResponsesAreWellFramed(t *testing.T) {
	for _, code := range []int{400, 403, 404, 413, 429, 431, 500, 503, 599} {
		for _, retry := range []int{0, 7} {
			var w bytes.Buffer
			writeFixedStatus(&cannedSink{w: &w}, code, retry)
			resp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(w.Bytes())), nil)
			if err != nil {
				t.Fatalf("%d/%d: %v", code, retry, err)
			}
			body, _ := io.ReadAll(resp.Body)
			if int64(len(body)) != resp.ContentLength || resp.StatusCode != code {
				t.Fatalf("%d/%d: status %d body %d bytes, Content-Length %d", code, retry, resp.StatusCode, len(body), resp.ContentLength)
			}
		}
	}
}
