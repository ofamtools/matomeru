package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// TestParallelTests enforces parallel by default, even in build-tagged files.
func TestParallelTests(t *testing.T) {
	t.Parallel()
	paths, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !strings.HasPrefix(fn.Name.Name, "Test") || fn.Name.Name == "TestMain" || fn.Body == nil {
				continue
			}
			if fn.Doc != nil && strings.Contains(fn.Doc.Text(), "serial: ") {
				for _, line := range strings.Split(fn.Doc.Text(), "\n") {
					if strings.HasPrefix(line, "serial: ") && strings.TrimSpace(strings.TrimPrefix(line, "serial: ")) != "" {
						goto nextTest
					}
				}
			}
			if len(fn.Body.List) != 0 {
				if stmt, ok := fn.Body.List[0].(*ast.ExprStmt); ok {
					if call, ok := stmt.X.(*ast.CallExpr); ok && len(call.Args) == 0 {
						if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Parallel" {
							if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == "t" {
								goto nextTest
							}
						}
					}
				}
			}
			t.Errorf("%s: %s must begin with t.Parallel() or have // serial: <reason>", path, fn.Name.Name)
		nextTest:
		}
	}
}
