package rawhttp

import (
	"fmt"
	"net"
)

// ListenReusePort creates a TCP listener on network/address.
// On Linux/BSD it enables SO_REUSEPORT; elsewhere it falls back to net.Listen.
func ListenReusePort(network, address string) (net.Listener, error) {
	if network == "" {
		network = "tcp"
	}
	ln, err := reusePortListen(network, address)
	if err != nil {
		return nil, fmt.Errorf("rawhttp: reuseport: %w", err)
	}
	return ln, nil
}
