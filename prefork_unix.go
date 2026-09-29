//go:build unix

package rawhttp

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

func servePrefork(s *Server, cfg PreforkConfig, workers int) error {
	if PreforkIsChild() {
		return preforkServeChild(s, cfg)
	}

	// Master: either reuseport (each child listens) or inherit one fd.
	if cfg.ReusePort {
		return preforkMasterReusePort(cfg, workers)
	}
	return preforkMasterInherit(s, cfg, workers)
}

func preforkMasterReusePort(cfg PreforkConfig, workers int) error {
	children := make([]*exec.Cmd, 0, workers)
	for i := 0; i < workers; i++ {
		// Re-exec this binary as a worker; args come from the current process only.
		cmd := exec.Command(os.Args[0], os.Args[1:]...) //nolint:gosec // G204: intentional self re-exec for prefork
		cmd.Env = append(os.Environ(), "RAWHTTP_PREFORK_CHILD=1")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Stdin = os.Stdin
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("rawhttp: prefork start: %w", err)
		}
		children = append(children, cmd)
	}

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch
	for _, c := range children {
		_ = c.Process.Signal(syscall.SIGTERM)
	}
	for _, c := range children {
		_ = c.Wait()
	}
	return nil
}

func preforkMasterInherit(s *Server, cfg PreforkConfig, workers int) error {
	ln, err := net.Listen(cfg.Network, cfg.Addr)
	if err != nil {
		return err
	}
	defer func() { _ = ln.Close() }()

	tcpln, ok := ln.(*net.TCPListener)
	if !ok {
		return fmt.Errorf("rawhttp: prefork: need TCPListener for fd inheritance")
	}
	f, err := tcpln.File()
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	children := make([]*exec.Cmd, 0, workers)
	for i := 0; i < workers; i++ {
		// Re-exec this binary as a worker; args come from the current process only.
		cmd := exec.Command(os.Args[0], os.Args[1:]...) //nolint:gosec // G204: intentional self re-exec for prefork
		cmd.Env = append(os.Environ(),
			"RAWHTTP_PREFORK_CHILD=1",
			"RAWHTTP_PREFORK_FD=3",
		)
		cmd.ExtraFiles = []*os.File{f}
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			return err
		}
		children = append(children, cmd)
	}

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch
	for _, c := range children {
		_ = c.Process.Signal(syscall.SIGTERM)
	}
	for _, c := range children {
		_ = c.Wait()
	}
	return nil
}

func preforkServeChild(s *Server, cfg PreforkConfig) error {
	if fd := os.Getenv("RAWHTTP_PREFORK_FD"); fd != "" {
		f := os.NewFile(3, "listener")
		if f == nil {
			return fmt.Errorf("rawhttp: prefork: invalid inherited fd")
		}
		ln, err := net.FileListener(f)
		_ = f.Close()
		if err != nil {
			return err
		}
		return s.Serve(ln)
	}
	ln, err := ListenReusePort(cfg.Network, cfg.Addr)
	if err != nil {
		return err
	}
	return s.Serve(ln)
}
