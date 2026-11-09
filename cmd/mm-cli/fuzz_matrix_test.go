package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// TestFuzzMatrix: the matrix of .github/workflows/fuzz.yml names every fuzz
// target of internal/, and nothing else. go test -fuzz with a name that
// matches no target passes without fuzzing, so a renamed target would stop
// being fuzzed while the job stays green.
func TestFuzzMatrix(t *testing.T) {
	wf, err := os.ReadFile("../../.github/workflows/fuzz.yml")
	if err != nil {
		t.Fatal(err)
	}
	var inCI []string
	for _, m := range regexp.MustCompile(`\{ pkg: (\w+), name: (Fuzz\w+) \}`).FindAllStringSubmatch(string(wf), -1) {
		inCI = append(inCI, m[1]+"."+m[2])
	}
	files, err := filepath.Glob("../../internal/*/*_test.go")
	if err != nil {
		t.Fatal(err)
	}
	var inCode []string
	fset := token.NewFileSet()
	for _, f := range files {
		file, err := parser.ParseFile(fset, f, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range file.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok && fn.Recv == nil && strings.HasPrefix(fn.Name.Name, "Fuzz") {
				inCode = append(inCode, filepath.Base(filepath.Dir(f))+"."+fn.Name.Name)
			}
		}
	}
	slices.Sort(inCI)
	slices.Sort(inCode)
	if len(inCode) == 0 || !slices.Equal(inCI, inCode) {
		t.Fatalf("fuzz.yml matrix %v, fuzz targets in internal/ %v", inCI, inCode)
	}
}
