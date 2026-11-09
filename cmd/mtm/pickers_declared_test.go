package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The owner's rule (2026-11-09): mtm has one general picker configured by
// data (pickers.go: pickSpec, choose, askParameter), the path chooser for
// files and directories, and the command palette. Nothing else opens a
// chooser or builds a picker row by hand. The allow lists below are the
// exceptions, each with its reason; a stale entry fails too, so the list
// only shrinks.

// openAllowed are the non-test files that may call the chooser openers.
var openAllowed = map[string]struct {
	calls []string
	why   string
}{
	"pickers.go":      {[]string{"OpenPicker"}, "the one general picker: choose opens every declared or asked chooser"},
	"path_sources.go": {[]string{"OpenPathChooser"}, "the path chooser: what picks a file or directory (owner exception)"},
}

// rowLiteralAllowed are the non-test files that may build a PickerRow by
// hand.
var rowLiteralAllowed = map[string]string{
	"directories.go":  "known and base directory rows: paths, to move onto the path chooser's declared sources (lane PU4)",
	"path_sources.go": "the path chooser's project-directory rows (owner exception; lane PU4)",
}

func mtmSourceFiles(t *testing.T) map[string]*ast.File {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	out := map[string]*ast.File{}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		out[name] = f
	}
	return out
}

func TestPickersDeclared(t *testing.T) {
	t.Parallel()
	openers := []string{"OpenChooser", "OpenPicker", "OpenPathChooser"}
	called := map[string][]string{}
	literal := map[string]bool{}
	for name, f := range mtmSourceFiles(t) {
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.CallExpr:
				if sel, ok := n.Fun.(*ast.SelectorExpr); ok && slices.Contains(openers, sel.Sel.Name) {
					called[name] = append(called[name], sel.Sel.Name)
				}
			case *ast.CompositeLit:
				if sel, ok := n.Type.(*ast.SelectorExpr); ok && (sel.Sel.Name == "PickerRow" || sel.Sel.Name == "ChooserRow") {
					literal[name] = true
				}
			}
			return true
		})
	}
	for name, calls := range called {
		allowed, ok := openAllowed[name]
		if !ok {
			t.Errorf("%s opens a chooser by hand (%v): declare it (pickSpec, askParameter) instead", name, calls)
			continue
		}
		for _, c := range calls {
			if !slices.Contains(allowed.calls, c) {
				t.Errorf("%s calls %s; allowed here: %v (%s)", name, c, allowed.calls, allowed.why)
			}
		}
	}
	for name, a := range openAllowed {
		if len(called[name]) == 0 {
			t.Errorf("%s no longer opens a chooser: drop it from openAllowed (%s)", name, a.why)
		}
	}
	for name := range literal {
		if _, ok := rowLiteralAllowed[name]; !ok {
			t.Errorf("%s builds a PickerRow by hand: return ochazuke.Choice from a source (Choice.Row is the one builder)", name)
		}
	}
	for name, why := range rowLiteralAllowed {
		if !literal[name] {
			t.Errorf("%s no longer builds PickerRow literals: drop it from rowLiteralAllowed (%s)", name, why)
		}
	}
}
