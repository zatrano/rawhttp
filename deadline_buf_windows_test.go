//go:build windows

package rawhttp_test

import (
	"fmt"
	"net"
	"syscall"
	"unsafe"
)

// SIO_LOOPBACK_FAST_PATH. Windows loopback otherwise accepts tens of MiB
// without honoring a small socket buffer. Zero disables the fast path and
// must be set before connect.
const sioLoopbackFastPath = 0x98000010

var loopbackFastPathErr error
var serverSnd int
var serverSndErr error

func smallRecvControl(network, address string, c syscall.RawConn) error {
	return c.Control(func(fd uintptr) {
		h := syscall.Handle(fd)
		var off int32
		var n uint32
		loopbackFastPathErr = syscall.WSAIoctl(h, sioLoopbackFastPath, (*byte)(unsafe.Pointer(&off)), 4, nil, 0, &n, nil, 0)
		_ = syscall.SetsockoptInt(h, syscall.SOL_SOCKET, syscall.SO_RCVBUF, 2048)
	})
}

func tcpBufferDetail(c net.Conn) string {
	rcv, snd, err := socketBuffers(c)
	return fmt.Sprintf("rcv=%d snd=%d buf=%v fast=%v serverSnd=%d serverErr=%v", rcv, snd, err, loopbackFastPathErr, serverSnd, serverSndErr)
}

func socketBuffers(c net.Conn) (rcv, snd int, err error) {
	tcp, ok := c.(*net.TCPConn)
	if !ok {
		return 0, 0, nil
	}
	raw, err := tcp.SyscallConn()
	if err != nil {
		return 0, 0, err
	}
	var rcvErr, sndErr error
	err = raw.Control(func(fd uintptr) {
		h := syscall.Handle(fd)
		rcv, rcvErr = syscall.GetsockoptInt(h, syscall.SOL_SOCKET, syscall.SO_RCVBUF)
		snd, sndErr = syscall.GetsockoptInt(h, syscall.SOL_SOCKET, syscall.SO_SNDBUF)
	})
	if rcvErr != nil {
		err = rcvErr
	}
	if sndErr != nil {
		err = sndErr
	}
	return rcv, snd, err
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
				h := syscall.Handle(fd)
				serverSndErr = syscall.SetsockoptInt(h, syscall.SOL_SOCKET, syscall.SO_SNDBUF, 2048)
				serverSnd, _ = syscall.GetsockoptInt(h, syscall.SOL_SOCKET, syscall.SO_SNDBUF)
			})
		}
	}
	return c, nil
}
