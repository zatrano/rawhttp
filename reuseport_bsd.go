//go:build darwin || freebsd || netbsd || openbsd

package rawhttp

import (
	"context"
	"net"
	"syscall"
)

// SO_REUSEPORT on BSD/Darwin.
const soReusePort = 0x200

func reusePortListen(network, address string) (net.Listener, error) {
	cfg := net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			var opErr error
			if err := c.Control(func(fd uintptr) {
				opErr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
				if opErr != nil {
					return
				}
				opErr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, soReusePort, 1)
			}); err != nil {
				return err
			}
			return opErr
		},
	}
	return cfg.Listen(context.Background(), network, address)
}
