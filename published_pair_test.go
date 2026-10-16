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
		"| 64 | 1.016 | 1.014–1.019 | 0.985 | 0.966 | 0.977 | ahead |",
		"| 256 | 1.028 | 1.024–1.038 | 0.997 | 0.954 | 0.922 | ahead |",
		"| 1024 | 1.128 | 1.077–1.146 | 0.862 | 0.883 | 0.911 | ahead |",
		"| 4096 | 1.093 | 1.086–1.104 | 0.917 | 0.911 | 0.816 | ahead |",
	} {
		if !strings.Contains(text, row) {
			t.Fatalf("README is missing the rawhttp/fasthttp row %s", row)
		}
	}
	if !strings.Contains(text, "go get github.com/zatrano/rawhttp@v1.0.1") {
		t.Fatal("README install line must name v1.0.1")
	}
}
