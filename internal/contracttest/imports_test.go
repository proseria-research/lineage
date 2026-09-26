package contracttest_test

import (
	"go/build"
	"strings"
	"testing"
)

// The client half must stay an outsider: the standard library and nothing else. That rules
// out internal/core, internal/domain and every store — and anything that might import them —
// without having to keep a deny-list current. Test files of the external package (the
// server half) are exempt; an in-package test file is not, since it compiles into the same
// package the client lives in.
func TestClientImportsOnlyTheStandardLibrary(t *testing.T) {
	pkg, err := build.ImportDir(".", 0)
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Name != "contracttest" {
		t.Fatalf("package = %q", pkg.Name)
	}
	for _, imp := range append(append([]string{}, pkg.Imports...), pkg.TestImports...) {
		if !isStdlib(imp) {
			t.Errorf("client half imports %q: /v1 over net/http is the only channel it may use", imp)
		}
	}
	if len(pkg.GoFiles) == 0 {
		t.Fatal("no client files found; the check would pass vacuously")
	}
}

// isStdlib reports whether path is a standard-library import: its first element has no dot,
// which every module path (github.com/…, golang.org/x/…) does.
func isStdlib(path string) bool {
	first, _, _ := strings.Cut(path, "/")
	return !strings.Contains(first, ".")
}
