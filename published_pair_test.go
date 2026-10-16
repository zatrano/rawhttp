package rawhttp_test

import (
	"os"
	"strings"
	"testing"
)

// The ServeConn comparison and TestGate_* live in the bench module.
// This test locks the published Linux TCP table for this repository.
func TestPublishedLinuxPair(t *testing.T) {
	raw, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, row := range []string{
		"| 64 | ustun | rps | 1.016 |",
		"| 256 | ustun | rps | 1.028 |",
		"| 1024 | ustun | hedef | 1.128 |",
		"| 4096 | ustun | hedef | 1.093 |",
	} {
		if !strings.Contains(text, row) {
			t.Fatalf("README is missing the rawhttp/fasthttp row %s", row)
		}
	}
	if !strings.Contains(text, "go get github.com/zatrano/rawhttp@v1.0.1") {
		t.Fatal("README install line must name v1.0.1")
	}
}
