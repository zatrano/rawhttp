//go:build !unix

package rawhttp

func servePrefork(s *Server, cfg PreforkConfig, workers int) error {
	// No fork: single process.
	_ = workers
	return preforkServeOne(s, cfg)
}
