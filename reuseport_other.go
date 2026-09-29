//go:build !linux && !darwin && !freebsd && !netbsd && !openbsd

package rawhttp

import "net"

func reusePortListen(network, address string) (net.Listener, error) {
	return net.Listen(network, address)
}
