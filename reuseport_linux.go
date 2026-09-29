//go:build linux

package rawhttp

import (
	"context"
	"net"
	"syscall"
)

const soReusePort = 0xf // SO_REUSEPORT on Linux

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
