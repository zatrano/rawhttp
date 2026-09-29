package rawhttp

import (
	"fmt"
	"net"
	"os"
	"runtime"
)

// PreforkConfig controls prefork behavior.
type PreforkConfig struct {
	// Network is passed to the listener (default "tcp").
	Network string
	// Addr is the listen address (e.g. ":8080"). Required.
	Addr string
	// Workers is the number of child processes. Zero → GOMAXPROCS.
	Workers int
	// ReusePort uses ListenReusePort instead of a shared inherited fd.
	// Recommended when Workers > 1 on Linux.
	ReusePort bool
}

// Prefork starts the server using multiple OS processes when possible.
// On Windows (or Workers == 1) it runs a single process.
func Prefork(s *Server, cfg PreforkConfig) error {
	if s == nil {
		return fmt.Errorf("rawhttp: prefork: nil server")
	}
	if cfg.Addr == "" {
		return fmt.Errorf("rawhttp: prefork: empty Addr")
	}
	if cfg.Network == "" {
		cfg.Network = "tcp"
	}
	n := cfg.Workers
	if n <= 0 {
		n = runtime.GOMAXPROCS(0)
		if n < 1 {
			n = 1
		}
	}

	if runtime.GOOS == "windows" || n == 1 {
		return preforkServeOne(s, cfg)
	}
	return servePrefork(s, cfg, n)
}

func preforkServeOne(s *Server, cfg PreforkConfig) error {
	var ln net.Listener
	var err error
	if cfg.ReusePort {
		ln, err = ListenReusePort(cfg.Network, cfg.Addr)
	} else {
		ln, err = net.Listen(cfg.Network, cfg.Addr)
	}
	if err != nil {
		return err
	}
	return s.Serve(ln)
}

// PreforkIsChild reports whether the current process is a prefork worker.
func PreforkIsChild() bool {
	return os.Getenv("RAWHTTP_PREFORK_CHILD") == "1"
}
