package rawhttp_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// unsafeAllowlist lists the files (slash separated, relative to the module
// root) that may import unsafe, with the only unsafe selectors they may use.
// Adding an entry is a reviewed decision that also needs a line in
// docs/SECURITY-GUARANTEES.md.
var unsafeAllowlist = map[string][]string{
	// sliceOverlaps compares the addresses of two byte slices; it never
	// builds a pointer or a slice from an address.
	"ctx.go": {"Pointer"},
	// The Windows socket buffer test passes pointers to WSAIoctl.
	"deadline_buf_windows_test.go": {"Pointer"},
}

// TestUnsafeOnlyInAllowlistedFiles enforces invariant I5 for rawhttp: unsafe
// and //go:linkname appear only in allowlisted files, and those files use
// only the listed unsafe selectors. Stale entries fail too.
func TestUnsafeOnlyInAllowlistedFiles(t *testing.T) {
	var hits []string
	seen := map[string]bool{}
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "vendor", "node_modules", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel := filepath.ToSlash(path)
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ParseComments)
		if err != nil {
			return err
		}
		for _, group := range file.Comments {
			for _, c := range group.List {
				if strings.HasPrefix(c.Text, "//go:linkname") {
					hits = append(hits, rel+": "+c.Text)
				}
			}
		}
		local := ""
		for _, spec := range file.Imports {
			if p, _ := strconv.Unquote(spec.Path.Value); p == "unsafe" {
				local = "unsafe"
				if spec.Name != nil {
					local = spec.Name.Name
				}
			}
		}
		if local == "" {
			return nil
		}
		allowed, ok := unsafeAllowlist[rel]
		if !ok {
			hits = append(hits, rel+": import \"unsafe\"")
			return nil
		}
		seen[rel] = true
		ast.Inspect(file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == local {
				for _, a := range allowed {
					if sel.Sel.Name == a {
						return true
					}
				}
				hits = append(hits, rel+": unsafe."+sel.Sel.Name+" is not allowlisted")
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for f := range unsafeAllowlist {
		if !seen[f] {
			hits = append(hits, f+": allowlisted but no longer imports unsafe; remove the entry")
		}
	}
	if len(hits) > 0 {
		sort.Strings(hits)
		t.Fatalf("unsafe policy violations:\n%s", strings.Join(hits, "\n"))
	}
}
