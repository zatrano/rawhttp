package rawhttp

import (
	"bufio"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"time"
)

// HTTPProxyDial returns a Dial func that tunnels via an HTTP CONNECT proxy.
// proxyAddr is host:port or user:pass@host:port (no scheme).
func HTTPProxyDial(proxyAddr string) func(network, addr string) (net.Conn, error) {
	return HTTPProxyDialTimeout(proxyAddr, DefaultDialTimeout)
}

// HTTPProxyDialTimeout is HTTPProxyDial with an explicit timeout.
func HTTPProxyDialTimeout(proxyAddr string, timeout time.Duration) func(network, addr string) (net.Conn, error) {
	user, pass, host := splitProxyAuth(proxyAddr)
	return func(network, addr string) (net.Conn, error) {
		_ = network
		return dialHTTPProxy(host, addr, user, pass, timeout)
	}
}

// SOCKS5ProxyDial returns a Dial func for a SOCKS5 proxy.
// proxyAddr is host:port or user:pass@host:port (optional socks5:// prefix).
func SOCKS5ProxyDial(proxyAddr string) func(network, addr string) (net.Conn, error) {
	return SOCKS5ProxyDialTimeout(proxyAddr, DefaultDialTimeout)
}

// SOCKS5ProxyDialTimeout is SOCKS5ProxyDial with an explicit timeout.
func SOCKS5ProxyDialTimeout(proxyAddr string, timeout time.Duration) func(network, addr string) (net.Conn, error) {
	proxyAddr = strings.TrimPrefix(proxyAddr, "socks5://")
	proxyAddr = strings.TrimPrefix(proxyAddr, "socks5h://")
	user, pass, host := splitProxyAuth(proxyAddr)
	return func(network, addr string) (net.Conn, error) {
		_ = network
		return dialSOCKS5(host, addr, user, pass, timeout)
	}
}

func splitProxyAuth(proxyAddr string) (user, pass, host string) {
	proxyAddr = strings.TrimPrefix(proxyAddr, "http://")
	proxyAddr = strings.TrimPrefix(proxyAddr, "https://")
	if u, err := url.Parse("http://" + proxyAddr); err == nil && u.Host != "" {
		host = u.Host
		if u.User != nil {
			user = u.User.Username()
			pass, _ = u.User.Password()
		}
		return user, pass, host
	}
	return "", "", proxyAddr
}

func dialHTTPProxy(proxyHost, target, user, pass string, timeout time.Duration) (net.Conn, error) {
	if timeout <= 0 {
		timeout = DefaultDialTimeout
	}
	conn, err := net.DialTimeout("tcp", proxyHost, timeout)
	if err != nil {
		return nil, err
	}
	_ = conn.SetDeadline(time.Now().Add(timeout))

	req := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n", target, target)
	if user != "" {
		token := basicAuthToken(user, pass)
		req += "Proxy-Authorization: Basic " + token + "\r\n"
	}
	req += "\r\n"
	if _, err = io.WriteString(conn, req); err != nil {
		_ = conn.Close()
		return nil, err
	}
	br := bufio.NewReader(conn)
	line, err := br.ReadString('\n')
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	// HTTP/1.x 200 ...
	if !strings.Contains(line, " 200") {
		_ = conn.Close()
		return nil, fmt.Errorf("rawhttp: proxy CONNECT failed: %s", strings.TrimSpace(line))
	}
	for {
		h, err := br.ReadString('\n')
		if err != nil {
			_ = conn.Close()
			return nil, err
		}
		if h == "\r\n" || h == "\n" {
			break
		}
	}
	_ = conn.SetDeadline(time.Time{})
	if br.Buffered() > 0 {
		return &bufConn{Conn: conn, r: br}, nil
	}
	return conn, nil
}

type bufConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufConn) Read(p []byte) (int, error) { return c.r.Read(p) }

func basicAuthToken(user, pass string) string {
	return base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
}

func dialSOCKS5(proxyHost, target, user, pass string, timeout time.Duration) (net.Conn, error) {
	if timeout <= 0 {
		timeout = DefaultDialTimeout
	}
	conn, err := net.DialTimeout("tcp", proxyHost, timeout)
	if err != nil {
		return nil, err
	}
	_ = conn.SetDeadline(time.Now().Add(timeout))

	// greeting
	if user != "" {
		if _, err = conn.Write([]byte{0x05, 0x02, 0x00, 0x02}); err != nil {
			_ = conn.Close()
			return nil, err
		}
	} else {
		if _, err = conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
			_ = conn.Close()
			return nil, err
		}
	}
	var greet [2]byte
	if _, err = io.ReadFull(conn, greet[:]); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if greet[0] != 0x05 {
		_ = conn.Close()
		return nil, errors.New("rawhttp: invalid SOCKS5 version")
	}
	switch greet[1] {
	case 0x00: // no auth
	case 0x02: // username/password
		if user == "" {
			_ = conn.Close()
			return nil, errors.New("rawhttp: SOCKS5 authentication required")
		}
		ub, pb := []byte(user), []byte(pass)
		if len(ub) > 255 || len(pb) > 255 {
			_ = conn.Close()
			return nil, errors.New("rawhttp: SOCKS5 credentials too long")
		}
		msg := make([]byte, 0, 3+len(ub)+len(pb))
		msg = append(msg, 0x01, byte(len(ub)))
		msg = append(msg, ub...)
		msg = append(msg, byte(len(pb)))
		msg = append(msg, pb...)
		if _, err = conn.Write(msg); err != nil {
			_ = conn.Close()
			return nil, err
		}
		var auth [2]byte
		if _, err = io.ReadFull(conn, auth[:]); err != nil {
			_ = conn.Close()
			return nil, err
		}
		if auth[1] != 0 {
			_ = conn.Close()
			return nil, errors.New("rawhttp: SOCKS5 authentication failed")
		}
	default:
		_ = conn.Close()
		return nil, errors.New("rawhttp: unsupported SOCKS5 auth method")
	}

	host, portStr, err := net.SplitHostPort(target)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	port, err := net.LookupPort("tcp", portStr)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if port < 0 || port > 0xffff {
		_ = conn.Close()
		return nil, errors.New("rawhttp: SOCKS5 invalid port")
	}
	uport := uint16(port) //nolint:gosec // G115: range checked above

	req := []byte{0x05, 0x01, 0x00}
	if ip := net.ParseIP(host); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			req = append(req, 0x01)
			req = append(req, ip4...)
		} else {
			req = append(req, 0x04)
			req = append(req, ip...)
		}
	} else {
		if len(host) > 255 {
			_ = conn.Close()
			return nil, errors.New("rawhttp: SOCKS5 host too long")
		}
		req = append(req, 0x03, byte(len(host)))
		req = append(req, host...)
	}
	var pb [2]byte
	binary.BigEndian.PutUint16(pb[:], uport)
	req = append(req, pb[:]...)
	if _, err = conn.Write(req); err != nil {
		_ = conn.Close()
		return nil, err
	}

	var hdr [4]byte
	if _, err = io.ReadFull(conn, hdr[:]); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if hdr[0] != 0x05 || hdr[1] != 0x00 {
		_ = conn.Close()
		return nil, fmt.Errorf("rawhttp: SOCKS5 connect failed: status=%d", hdr[1])
	}
	switch hdr[3] {
	case 0x01:
		var skip [4 + 2]byte
		if _, err = io.ReadFull(conn, skip[:]); err != nil {
			_ = conn.Close()
			return nil, err
		}
	case 0x03:
		var n [1]byte
		if _, err = io.ReadFull(conn, n[:]); err != nil {
			_ = conn.Close()
			return nil, err
		}
		skip := make([]byte, int(n[0])+2)
		if _, err = io.ReadFull(conn, skip); err != nil {
			_ = conn.Close()
			return nil, err
		}
	case 0x04:
		var skip [16 + 2]byte
		if _, err = io.ReadFull(conn, skip[:]); err != nil {
			_ = conn.Close()
			return nil, err
		}
	default:
		_ = conn.Close()
		return nil, errors.New("rawhttp: invalid SOCKS5 address type")
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, nil
}
