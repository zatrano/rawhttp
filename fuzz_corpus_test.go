package rawhttp_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/zatrano/rawhttp/tests/smuggling"
)

// The persistent fuzz corpus under testdata/fuzz holds every smuggling case,
// so plain `go test` replays them through the fuzz targets and each fuzz run
// starts from them. Regenerate with RAWHTTP_WRITE_FUZZ_CORPUS=1.

type corpusTarget struct {
	dir     string
	twoArgs bool // FuzzDifferential* targets take (data []byte, allowUpgrade bool)
}

var corpusTargets = []corpusTarget{
	{dir: "testdata/fuzz/FuzzServeConn"},
	{dir: "testdata/fuzz/FuzzDifferentialNetHTTP", twoArgs: true},
	{dir: "differential/testdata/fuzz/FuzzDifferentialFraming"},
}

func corpusFileName(name string) string {
	return "smuggling-" + strings.NewReplacer("/", "-", " ", "-").Replace(name)
}

func corpusFile(raw string, twoArgs bool) string {
	s := "go test fuzz v1\n[]byte(" + strconv.Quote(raw) + ")\n"
	if twoArgs {
		s += "bool(false)\n"
	}
	return s
}

func TestWriteFuzzCorpus(t *testing.T) {
	if os.Getenv("RAWHTTP_WRITE_FUZZ_CORPUS") != "1" {
		t.Skip("set RAWHTTP_WRITE_FUZZ_CORPUS=1 to regenerate testdata/fuzz")
	}
	for _, target := range corpusTargets {
		if err := os.MkdirAll(target.dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, c := range smuggling.Cases() {
			path := filepath.Join(target.dir, corpusFileName(c.Name))
			if err := os.WriteFile(path, []byte(corpusFile(c.Raw, target.twoArgs)), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestFuzzCorpusCoversSmugglingCases(t *testing.T) {
	if os.Getenv("RAWHTTP_WRITE_FUZZ_CORPUS") == "1" {
		t.Skip("corpus is being regenerated")
	}
	var missing []string
	for _, target := range corpusTargets {
		for _, c := range smuggling.Cases() {
			path := filepath.Join(target.dir, corpusFileName(c.Name))
			got, err := os.ReadFile(path)
			if err != nil || strings.ReplaceAll(string(got), "\r\n", "\n") != corpusFile(c.Raw, target.twoArgs) {
				missing = append(missing, path)
			}
		}
	}
	if len(missing) > 0 {
		t.Fatalf("fuzz corpus out of date (run with RAWHTTP_WRITE_FUZZ_CORPUS=1):\n%s", strings.Join(missing, "\n"))
	}
}
