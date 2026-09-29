package rawhttp

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"strconv"
	"sync"
)

var streamCopyBufPool = sync.Pool{New: func() any {
	b := make([]byte, 32<<10)
	return &b
}}

var streamWriterPool = sync.Pool{New: func() any {
	return bufio.NewWriterSize(nil, 4096)
}}

func acquireStreamWriter(w io.Writer, size int) *bufio.Writer {
	bw := streamWriterPool.Get().(*bufio.Writer)
	if size > 0 && bw.Size() != size {
		bw = bufio.NewWriterSize(w, size)
	} else {
		bw.Reset(w)
	}
	return bw
}

func releaseStreamWriter(bw *bufio.Writer) {
	bw.Reset(nil)
	streamWriterPool.Put(bw)
}

var (
	status200      = []byte("HTTP/1.1 200 OK\r\n")
	status200Plain = []byte("HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\n")
	status400      = []byte("HTTP/1.1 400 Bad Request\r\nContent-Type: text/plain\r\nConnection: close\r\nContent-Length: 11\r\n\r\nBad Request")
	status413      = []byte("HTTP/1.1 413 Request Entity Too Large\r\nContent-Type: text/plain\r\nConnection: close\r\nContent-Length: 17\r\n\r\nEntity Too Large")
	status414      = []byte("HTTP/1.1 414 URI Too Long\r\nContent-Type: text/plain\r\nConnection: close\r\nContent-Length: 12\r\n\r\nURI Too Long")
	status417      = []byte("HTTP/1.1 417 Expectation Failed\r\nContent-Type: text/plain\r\nConnection: close\r\nContent-Length: 19\r\n\r\nExpectation Failed")
	status421      = []byte("HTTP/1.1 421 Misdirected Request\r\nContent-Type: text/plain\r\nConnection: close\r\nContent-Length: 19\r\n\r\nMisdirected Request")
	status429      = []byte("HTTP/1.1 429 Too Many Requests\r\nContent-Type: text/plain\r\nConnection: close\r\nContent-Length: 17\r\n\r\nToo Many Requests")
	status431      = []byte("HTTP/1.1 431 Request Header Fields Too Large\r\nContent-Type: text/plain\r\nConnection: close\r\nContent-Length: 31\r\n\r\nRequest Header Fields Too Large")
	hdrCTPlain     = []byte("Content-Type: text/plain\r\n")
	hdrCL          = []byte("Content-Length: ")
	hdrTEChunk     = []byte("Transfer-Encoding: chunked\r\n")
	hdrConnKA      = []byte("Connection: keep-alive\r\n")
	hdrConnCl      = []byte("Connection: close\r\n")
	chunkEnd       = []byte("0\r\n\r\n")
	continue100    = []byte("HTTP/1.1 100 Continue\r\n\r\n")
)

func writeBadRequest(conn net.Conn) {
	_, _ = conn.Write(status400)
}

func writeEntityTooLarge(conn net.Conn) {
	_, _ = conn.Write(status413)
}

func writeURITooLong(conn net.Conn) {
	_, _ = conn.Write(status414)
}

func writeExpectationFailed(conn net.Conn) {
	_, _ = conn.Write(status417)
}

func writeTooManyRequests(conn net.Conn) {
	_, _ = conn.Write(status429)
}

func writeMisdirectedRequest(conn net.Conn) {
	_, _ = conn.Write(status421)
}

func writeHeaderTooLarge(conn net.Conn) {
	_, _ = conn.Write(status431)
}

func writeContinue(conn net.Conn) error {
	_, err := conn.Write(continue100)
	return err
}

func writeResponse(conn net.Conn, ctx *Ctx, closeConn bool) error {
	if ctx.streamBody != nil {
		return writeStreamResponse(conn, ctx, closeConn)
	}

	body := ctx.respBody
	n := len(body)
	head := ctx.head
	code := ctx.StatusCode
	noBodyStatus := code == 204 || code == 304 || code == 205 || (code >= 100 && code < 200)
	chunked := ctx.respChunked && !head && !noBodyStatus

	// Fastest path: keep-alive cache without Date (stable wire bytes).
	if !chunked && ctx.cacheOK && ctx.noDefaultDate && !ctx.noDefaultCT && !ctx.userSetCT &&
		len(ctx.respHeaderBuf) == 0 && ctx.bodyIsRef && code == 200 &&
		!closeConn && !head && n > 0 {
		if ctx.cachedCL == n && ctx.cachedCode == 200 &&
			ctx.cachedServer == ctx.serverName &&
			ctx.cachedBodyPtr != nil && &body[0] == ctx.cachedBodyPtr &&
			len(ctx.outBuf) > 0 && !ctx.cachedHasCT {
			_, err := conn.Write(ctx.outBuf)
			return err
		}
		ctx.outBuf = ctx.outBuf[:0]
		ctx.outBuf = append(ctx.outBuf, status200Plain...)
		ctx.outBuf = append(ctx.outBuf, hdrCL...)
		ctx.outBuf = strconv.AppendInt(ctx.outBuf, int64(n), 10)
		ctx.outBuf = append(ctx.outBuf, '\r', '\n')
		if ctx.serverName != "" {
			ctx.outBuf = append(ctx.outBuf, "Server: "...)
			ctx.outBuf = append(ctx.outBuf, ctx.serverName...)
			ctx.outBuf = append(ctx.outBuf, '\r', '\n')
		}
		ctx.outBuf = append(ctx.outBuf, '\r', '\n')
		ctx.outBuf = append(ctx.outBuf, body...)
		ctx.cachedCL = n
		ctx.cachedCode = 200
		ctx.cachedServer = ctx.serverName
		ctx.cachedBodyPtr = &body[0]
		ctx.cachedHasCT = false
		_, err := conn.Write(ctx.outBuf)
		return err
	}

	// Keep-alive cache with Content-Type (e.g. JSON echo) and no Date.
	if !chunked && ctx.cacheOK && ctx.noDefaultDate && ctx.userSetCT &&
		len(ctx.respHeaderBuf) == 0 && ctx.bodyIsRef && code == 200 &&
		!closeConn && !head && n > 0 && len(ctx.contentType) > 0 {
		if ctx.cachedCL == n && ctx.cachedCode == 200 &&
			ctx.cachedServer == ctx.serverName &&
			ctx.cachedHasCT &&
			bytes.Equal(ctx.cachedCT, ctx.contentType) &&
			ctx.cachedBodyPtr != nil && &body[0] == ctx.cachedBodyPtr &&
			len(ctx.outBuf) > 0 {
			_, err := conn.Write(ctx.outBuf)
			return err
		}
		ctx.outBuf = ctx.outBuf[:0]
		ctx.outBuf = append(ctx.outBuf, status200...)
		ctx.outBuf = append(ctx.outBuf, "Content-Type: "...)
		ctx.outBuf = append(ctx.outBuf, ctx.contentType...)
		ctx.outBuf = append(ctx.outBuf, '\r', '\n')
		ctx.outBuf = append(ctx.outBuf, hdrCL...)
		ctx.outBuf = strconv.AppendInt(ctx.outBuf, int64(n), 10)
		ctx.outBuf = append(ctx.outBuf, '\r', '\n')
		if ctx.serverName != "" {
			ctx.outBuf = append(ctx.outBuf, "Server: "...)
			ctx.outBuf = append(ctx.outBuf, ctx.serverName...)
			ctx.outBuf = append(ctx.outBuf, '\r', '\n')
		}
		ctx.outBuf = append(ctx.outBuf, '\r', '\n')
		ctx.outBuf = append(ctx.outBuf, body...)
		ctx.cachedCL = n
		ctx.cachedCode = 200
		ctx.cachedServer = ctx.serverName
		ctx.cachedBodyPtr = &body[0]
		ctx.cachedHasCT = true
		ctx.cachedCT = append(ctx.cachedCT[:0], ctx.contentType...)
		_, err := conn.Write(ctx.outBuf)
		return err
	}

	// Fast keep-alive cache with Date.
	if !chunked && ctx.cacheOK && !ctx.noDefaultCT && !ctx.userSetCT &&
		len(ctx.respHeaderBuf) == 0 && ctx.bodyIsRef && code == 200 &&
		!closeConn && !head && n > 0 && !ctx.noDefaultDate {
		dateSec := currentDateSec()
		if ctx.cachedCL == n && ctx.cachedCode == 200 &&
			ctx.cachedServer == ctx.serverName &&
			ctx.cachedBodyPtr != nil && &body[0] == ctx.cachedBodyPtr &&
			len(ctx.outBuf) > 0 {
			if ctx.cachedDateSec == dateSec {
				_, err := conn.Write(ctx.outBuf)
				return err
			}
			if ctx.cachedDateOff > 0 && ctx.cachedDateOff+dateHeaderLen <= len(ctx.outBuf) {
				date, _ := currentDateHeader()
				copy(ctx.outBuf[ctx.cachedDateOff:ctx.cachedDateOff+dateHeaderLen], date)
				ctx.cachedDateSec = dateSec
				_, err := conn.Write(ctx.outBuf)
				return err
			}
		}
		date, _ := currentDateHeader()
		ctx.outBuf = ctx.outBuf[:0]
		ctx.outBuf = append(ctx.outBuf, status200Plain...)
		ctx.outBuf = append(ctx.outBuf, hdrCL...)
		ctx.outBuf = strconv.AppendInt(ctx.outBuf, int64(n), 10)
		ctx.outBuf = append(ctx.outBuf, '\r', '\n')
		if ctx.serverName != "" {
			ctx.outBuf = append(ctx.outBuf, "Server: "...)
			ctx.outBuf = append(ctx.outBuf, ctx.serverName...)
			ctx.outBuf = append(ctx.outBuf, '\r', '\n')
		}
		ctx.cachedDateOff = len(ctx.outBuf)
		ctx.outBuf = append(ctx.outBuf, date...)
		ctx.outBuf = append(ctx.outBuf, '\r', '\n')
		ctx.outBuf = append(ctx.outBuf, body...)
		ctx.cachedCL = n
		ctx.cachedCode = 200
		ctx.cachedDateSec = dateSec
		ctx.cachedServer = ctx.serverName
		ctx.cachedBodyPtr = &body[0]
		ctx.cachedHasCT = false
		_, err := conn.Write(ctx.outBuf)
		return err
	}

	var date []byte
	if !ctx.noDefaultDate {
		date, _ = currentDateHeader()
	}

	if noBodyStatus {
		n = 0
		body = nil
	}

	ctx.outBuf = ctx.outBuf[:0]
	switch ctx.StatusCode {
	case 200:
		ctx.outBuf = append(ctx.outBuf, status200...)
	default:
		ctx.outBuf = append(ctx.outBuf, "HTTP/1.1 "...)
		ctx.outBuf = strconv.AppendInt(ctx.outBuf, int64(ctx.StatusCode), 10)
		ctx.outBuf = append(ctx.outBuf, ' ')
		ctx.outBuf = append(ctx.outBuf, statusText(ctx.StatusCode)...)
		ctx.outBuf = append(ctx.outBuf, '\r', '\n')
	}
	if len(ctx.respHeaderBuf) > 0 {
		ctx.outBuf = append(ctx.outBuf, ctx.respHeaderBuf...)
	}
	if ctx.userSetCT {
		ctx.outBuf = append(ctx.outBuf, "Content-Type: "...)
		ctx.outBuf = append(ctx.outBuf, ctx.contentType...)
		ctx.outBuf = append(ctx.outBuf, '\r', '\n')
	} else if !ctx.noDefaultCT && ctx.StatusCode == 200 && len(ctx.respHeaderBuf) == 0 {
		ctx.outBuf = append(ctx.outBuf, hdrCTPlain...)
	}
	if chunked {
		ctx.outBuf = append(ctx.outBuf, hdrTEChunk...)
	} else {
		ctx.outBuf = append(ctx.outBuf, hdrCL...)
		ctx.outBuf = strconv.AppendInt(ctx.outBuf, int64(n), 10)
		ctx.outBuf = append(ctx.outBuf, '\r', '\n')
	}
	if closeConn {
		ctx.outBuf = append(ctx.outBuf, hdrConnCl...)
	} else if ctx.httpMinor == 0 {
		ctx.outBuf = append(ctx.outBuf, hdrConnKA...)
	}
	if ctx.serverName != "" {
		ctx.outBuf = append(ctx.outBuf, "Server: "...)
		ctx.outBuf = append(ctx.outBuf, ctx.serverName...)
		ctx.outBuf = append(ctx.outBuf, '\r', '\n')
	}
	if date != nil {
		ctx.outBuf = append(ctx.outBuf, date...)
	}
	ctx.outBuf = append(ctx.outBuf, '\r', '\n')
	if !head && !noBodyStatus {
		ctx.outBuf = append(ctx.outBuf, body...)
		if chunked {
			ctx.outBuf = append(ctx.outBuf, chunkEnd...)
		}
	}
	_, err := conn.Write(ctx.outBuf)
	return err
}

func writeStreamResponse(conn net.Conn, ctx *Ctx, closeConn bool) error {
	head := ctx.head
	code := ctx.StatusCode
	noBodyStatus := code == 204 || code == 304 || code == 205 || (code >= 100 && code < 200)
	size := ctx.streamSize
	chunked := size < 0 && !head && !noBodyStatus

	var date []byte
	if !ctx.noDefaultDate {
		date, _ = currentDateHeader()
	}

	ctx.outBuf = ctx.outBuf[:0]
	switch ctx.StatusCode {
	case 200:
		ctx.outBuf = append(ctx.outBuf, status200...)
	default:
		ctx.outBuf = append(ctx.outBuf, "HTTP/1.1 "...)
		ctx.outBuf = strconv.AppendInt(ctx.outBuf, int64(ctx.StatusCode), 10)
		ctx.outBuf = append(ctx.outBuf, ' ')
		ctx.outBuf = append(ctx.outBuf, statusText(ctx.StatusCode)...)
		ctx.outBuf = append(ctx.outBuf, '\r', '\n')
	}
	if len(ctx.respHeaderBuf) > 0 {
		ctx.outBuf = append(ctx.outBuf, ctx.respHeaderBuf...)
	}
	if ctx.userSetCT {
		ctx.outBuf = append(ctx.outBuf, "Content-Type: "...)
		ctx.outBuf = append(ctx.outBuf, ctx.contentType...)
		ctx.outBuf = append(ctx.outBuf, '\r', '\n')
	} else if !ctx.noDefaultCT && ctx.StatusCode == 200 && len(ctx.respHeaderBuf) == 0 {
		ctx.outBuf = append(ctx.outBuf, hdrCTPlain...)
	}
	if noBodyStatus || head {
		ctx.outBuf = append(ctx.outBuf, hdrCL...)
		ctx.outBuf = append(ctx.outBuf, '0', '\r', '\n')
	} else if chunked {
		ctx.outBuf = append(ctx.outBuf, hdrTEChunk...)
	} else {
		ctx.outBuf = append(ctx.outBuf, hdrCL...)
		ctx.outBuf = strconv.AppendInt(ctx.outBuf, int64(size), 10)
		ctx.outBuf = append(ctx.outBuf, '\r', '\n')
	}
	if closeConn {
		ctx.outBuf = append(ctx.outBuf, hdrConnCl...)
	} else if ctx.httpMinor == 0 {
		ctx.outBuf = append(ctx.outBuf, hdrConnKA...)
	}
	if ctx.serverName != "" {
		ctx.outBuf = append(ctx.outBuf, "Server: "...)
		ctx.outBuf = append(ctx.outBuf, ctx.serverName...)
		ctx.outBuf = append(ctx.outBuf, '\r', '\n')
	}
	if date != nil {
		ctx.outBuf = append(ctx.outBuf, date...)
	}
	ctx.outBuf = append(ctx.outBuf, '\r', '\n')

	var (
		w  io.Writer = conn
		bw *bufio.Writer
	)
	if ctx.writeBufSize > 0 {
		bw = acquireStreamWriter(conn, ctx.writeBufSize)
		w = bw
		defer func() {
			_ = bw.Flush()
			releaseStreamWriter(bw)
		}()
	}

	if _, err := w.Write(ctx.outBuf); err != nil {
		return err
	}
	if head || noBodyStatus {
		if c, ok := ctx.streamBody.(io.Closer); ok {
			_ = c.Close()
		}
		return nil
	}

	bufPtr := streamCopyBufPool.Get().(*[]byte)
	buf := *bufPtr
	defer func() {
		streamCopyBufPool.Put(bufPtr)
		if c, ok := ctx.streamBody.(io.Closer); ok {
			_ = c.Close()
		}
	}()
	if chunked {
		for {
			n, err := ctx.streamBody.Read(buf)
			if n > 0 {
				ctx.outBuf = ctx.outBuf[:0]
				ctx.outBuf = appendChunk(ctx.outBuf, buf[:n])
				if _, werr := w.Write(ctx.outBuf); werr != nil {
					return werr
				}
			}
			if err != nil {
				if err == io.EOF {
					_, werr := w.Write(chunkEnd)
					return werr
				}
				return err
			}
		}
	}

	remaining := size
	for remaining > 0 {
		chunk := len(buf)
		if chunk > remaining {
			chunk = remaining
		}
		n, err := io.ReadFull(ctx.streamBody, buf[:chunk])
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return werr
			}
			remaining -= n
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func statusText(code int) string {
	return StatusText(code)
}

// StatusText returns a short English description for an HTTP status code.
func StatusText(code int) string {
	switch code {
	case 200:
		return "OK"
	case 201:
		return "Created"
	case 204:
		return "No Content"
	case 205:
		return "Reset Content"
	case 206:
		return "Partial Content"
	case 301:
		return "Moved Permanently"
	case 302:
		return "Found"
	case 304:
		return "Not Modified"
	case 400:
		return "Bad Request"
	case 401:
		return "Unauthorized"
	case 403:
		return "Forbidden"
	case 404:
		return "Not Found"
	case 405:
		return "Method Not Allowed"
	case 416:
		return "Range Not Satisfiable"
	case 408:
		return "Request Timeout"
	case 409:
		return "Conflict"
	case 412:
		return "Precondition Failed"
	case 413:
		return "Request Entity Too Large"
	case 414:
		return "URI Too Long"
	case 417:
		return "Expectation Failed"
	case 421:
		return "Misdirected Request"
	case 429:
		return "Too Many Requests"
	case 431:
		return "Request Header Fields Too Large"
	case 500:
		return "Internal Server Error"
	case 502:
		return "Bad Gateway"
	case 503:
		return "Service Unavailable"
	default:
		return "Status"
	}
}
