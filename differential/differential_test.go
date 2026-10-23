package differential_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zatrano/rawhttp"
	"github.com/zatrano/rawhttp/tests/smuggling"
)

type seen struct {
	method, target, body string
}

func (s seen) String() string { return fmt.Sprintf("%s %s body=%q", s.method, s.target, s.body) }

// memConn yields a fixed byte stream, then EOF, and discards what is written.
type memConn struct {
	mu     sync.Mutex
	r      *bytes.Reader
	closed chan struct{}
	once   sync.Once
}

func newMemConn(b []byte) *memConn {
	return &memConn{r: bytes.NewReader(b), closed: make(chan struct{})}
}

func (c *memConn) Read(b []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.r.Read(b)
}
func (c *memConn) Write(b []byte) (int, error) { return len(b), nil }
func (c *memConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return nil
}
func (c *memConn) LocalAddr() net.Addr              { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 80} }
func (c *memConn) RemoteAddr() net.Addr             { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 50000} }
func (c *memConn) SetDeadline(time.Time) error      { return nil }
func (c *memConn) SetReadDeadline(time.Time) error  { return nil }
func (c *memConn) SetWriteDeadline(time.Time) error { return nil }

func rawhttpSeen(raw []byte) []seen {
	var out []seen
	srv := &rawhttp.Server{
		ReadTimeout: -1, WriteTimeout: -1, IdleTimeout: -1,
		LingerTimeout:      time.Millisecond,
		MaxRequestBodySize: 1 << 20,
		Handler: func(ctx *rawhttp.Ctx) {
			target := string(ctx.Path)
			if ctx.Query != nil {
				target += "?" + string(ctx.Query)
			}
			out = append(out, seen{method: string(ctx.Method), target: target, body: string(ctx.Body())})
		},
	}
	_ = srv.ServeConn(newMemConn(raw))
	return out
}

type oneConnListener struct {
	mu   sync.Mutex
	conn net.Conn
	done chan struct{}
}

var errDone = errors.New("listener done")

func (l *oneConnListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	c := l.conn
	l.conn = nil
	l.mu.Unlock()
	if c != nil {
		return c, nil
	}
	<-l.done
	return nil, errDone
}
func (l *oneConnListener) Close() error   { return nil }
func (l *oneConnListener) Addr() net.Addr { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 80} }

func nethttpSeen(raw []byte) []seen {
	var mu sync.Mutex
	var out []seen
	conn := newMemConn(raw)
	ln := &oneConnListener{conn: conn, done: make(chan struct{})}
	srv := &http.Server{
		ReadHeaderTimeout: 5 * time.Second,
		ErrorLog:          log.New(io.Discard, "", 0),
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			if err != nil {
				// A body net/http could not read to the end was not framed
				// as a complete request.
				return
			}
			mu.Lock()
			out = append(out, seen{method: r.Method, target: r.RequestURI, body: string(body)})
			mu.Unlock()
		}),
		ConnState: func(_ net.Conn, st http.ConnState) {
			if st == http.StateClosed || st == http.StateHijacked {
				select {
				case <-ln.done:
				default:
					close(ln.done)
				}
			}
		},
	}
	go func() { _ = srv.Serve(ln) }()
	select {
	case <-ln.done:
	case <-time.After(10 * time.Second):
	}
	_ = srv.Close()
	mu.Lock()
	defer mu.Unlock()
	return append([]seen(nil), out...)
}

type verdict int

const (
	agree verdict = iota
	stricter
	disagree
)

func compare(rh, nh []seen) (verdict, string) {
	n := len(rh)
	if len(nh) < n {
		n = len(nh)
	}
	for i := 0; i < n; i++ {
		if rh[i] != nh[i] {
			return disagree, fmt.Sprintf("request %d: rawhttp %s, net/http %s", i, rh[i], nh[i])
		}
	}
	switch {
	case len(rh) > len(nh):
		// RFC 9112 asterisk-form is valid for OPTIONS; Go's net/http server
		// does not deliver it to the handler in this harness.
		if len(nh) == 0 && len(rh) == 1 && rh[0].method == "OPTIONS" && rh[0].target == "*" {
			return stricter, "OPTIONS * (RFC 9112 asterisk-form; net/http handler not invoked)"
		}
		return disagree, fmt.Sprintf("rawhttp served %d request(s), net/http %d; extra: %s", len(rh), len(nh), rh[len(nh)])
	case len(rh) < len(nh):
		return stricter, fmt.Sprintf("rawhttp served %d, net/http %d", len(rh), len(nh))
	}
	return agree, ""
}

func extraCases() []string {
	return []string{
		"GET /a?b=1 HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n",
		"POST /p HTTP/1.1\r\nHost: localhost\r\nContent-Length: 5\r\nConnection: close\r\n\r\nhello",
		"POST /p HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n5\r\nhello\r\n6\r\n world\r\n0\r\n\r\n",
		"GET /1 HTTP/1.1\r\nHost: localhost\r\n\r\nGET /2 HTTP/1.1\r\nHost: localhost\r\n\r\nGET /3 HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n",
		"POST /p HTTP/1.1\r\nHost: localhost\r\nContent-Length: 3\r\n\r\nabcGET /next HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n",
		"PUT /u HTTP/1.1\r\nHost: localhost\r\nExpect: 100-continue\r\nContent-Length: 2\r\nConnection: close\r\n\r\nok",
		"DELETE /d HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n",
		"OPTIONS * HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n",
		"GET / HTTP/1.0\r\nConnection: keep-alive\r\n\r\nGET /2 HTTP/1.0\r\n\r\n",
	}
}

// TestDifferentialAgainstNetHTTP runs the smuggling corpus and a few valid
// streams through both servers. Inputs where only net/http serves a request
// are logged as stricter, never failed.
func TestDifferentialAgainstNetHTTP(t *testing.T) {
	inputs := map[string]string{}
	for _, c := range smuggling.Cases() {
		inputs["corpus/"+c.Name] = c.Raw
	}
	for i, raw := range extraCases() {
		inputs[fmt.Sprintf("extra/%d", i)] = raw
	}
	var disagreements []string
	for name, raw := range inputs {
		rh := rawhttpSeen([]byte(raw))
		nh := nethttpSeen([]byte(raw))
		v, why := compare(rh, nh)
		switch v {
		case disagree:
			disagreements = append(disagreements, name+": "+why)
		case stricter:
			t.Logf("stricter %s: %s", name, why)
		}
	}
	if len(disagreements) > 0 {
		t.Fatalf("rawhttp and net/http frame %d input(s) differently:\n%s", len(disagreements), strings.Join(disagreements, "\n"))
	}
}

var mutationBytes = []byte{' ', '\t', '\r', '\n', ',', ':', ';', '0', '1', 'a', 'F', '\x00', '\x0b', '\x7f', 0xc4}

func mutate(rng *rand.Rand, raw []byte) []byte {
	b := append([]byte(nil), raw...)
	for n := 1 + rng.Intn(3); n > 0 && len(b) > 0; n-- {
		i := rng.Intn(len(b))
		switch rng.Intn(3) {
		case 0:
			b[i] = mutationBytes[rng.Intn(len(mutationBytes))]
		case 1:
			b = append(b[:i], b[i+1:]...)
		default:
			b = append(b[:i], append([]byte{mutationBytes[rng.Intn(len(mutationBytes))]}, b[i:]...)...)
		}
	}
	return b
}

// TestDifferentialMutations applies deterministic byte mutations to every
// accepted input and requires that rawhttp never frames a mutated stream in a
// way net/http does not.
func TestDifferentialMutations(t *testing.T) {
	var bases [][]byte
	for _, c := range smuggling.Cases() {
		if !c.Reject {
			bases = append(bases, []byte(c.Raw))
		}
	}
	for _, raw := range extraCases() {
		bases = append(bases, []byte(raw))
	}
	perBase := 200
	if testing.Short() {
		perBase = 20
	}
	rng := rand.New(rand.NewSource(20261020))
	var disagreements []string
	stricterCount, total := 0, 0
	for _, base := range bases {
		for i := 0; i < perBase; i++ {
			in := mutate(rng, base)
			total++
			v, why := compare(rawhttpSeen(in), nethttpSeen(in))
			switch v {
			case disagree:
				if len(disagreements) < 20 {
					disagreements = append(disagreements, fmt.Sprintf("%q\n  %s", in, why))
				}
			case stricter:
				stricterCount++
			}
		}
	}
	t.Logf("mutations=%d stricter=%d disagree=%d", total, stricterCount, len(disagreements))
	if len(disagreements) > 0 {
		t.Fatalf("framing disagreements:\n%s", strings.Join(disagreements, "\n"))
	}
}

func FuzzDifferentialFraming(f *testing.F) {
	for _, c := range smuggling.Cases() {
		f.Add([]byte(c.Raw))
	}
	for _, raw := range extraCases() {
		f.Add([]byte(raw))
	}
	f.Fuzz(func(t *testing.T, in []byte) {
		if len(in) == 0 || len(in) > 4<<10 {
			t.Skip()
		}
		if v, why := compare(rawhttpSeen(in), nethttpSeen(in)); v == disagree {
			t.Fatalf("framing disagreement on %q: %s", in, why)
		}
	})
}
