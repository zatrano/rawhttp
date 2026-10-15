//go:build !windows

package rawhttp_test

import (
	"fmt"
	"net"
	"syscall"
)

func smallRecvControl(network, address string, c syscall.RawConn) error {
	return c.Control(func(fd uintptr) {
		_ = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_RCVBUF, 2048)
	})
}

func tcpBufferDetail(c net.Conn) string {
	tcp, ok := c.(*net.TCPConn)
	if !ok {
		return "not tcp"
	}
	raw, err := tcp.SyscallConn()
	if err != nil {
		return err.Error()
	}
	var rcv, snd int
	_ = raw.Control(func(fd uintptr) {
		rcv, _ = syscall.GetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_RCVBUF)
		snd, _ = syscall.GetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_SNDBUF)
	})
	return fmt.Sprintf("rcv=%d snd=%d", rcv, snd)
}

func tightenRead(c net.Conn) {
	tcp, ok := c.(*net.TCPConn)
	if !ok {
		return
	}
	_ = tcp.SetReadBuffer(2048)
}

type smallSendListener struct {
	net.Listener
}

func (l smallSendListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	if tcp, ok := c.(*net.TCPConn); ok {
		_ = tcp.SetWriteBuffer(2048)
		if raw, err := tcp.SyscallConn(); err == nil {
			_ = raw.Control(func(fd uintptr) {
				_ = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_SNDBUF, 2048)
			})
		}
	}
	return c, nil
}
