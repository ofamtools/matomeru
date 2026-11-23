package matomeru_deps_test

import (
	"bufio"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var forbiddenFilenameRe = regexp.MustCompile(`(?i)(^|_)(wave|sprint|phase|milestone)[a-z0-9]*(_|\.go$)`)

func walkRepoGoFiles(t *testing.T, includeTests bool, fn func(relPath string)) {
	t.Helper()
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".worktrees", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") {
			return nil
		}
		if !includeTests && strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		fn(filepath.ToSlash(path))
		return nil
	})
	if err != nil {
		t.Fatalf("walk repo: %v", err)
	}
}

func countLines(t *testing.T, path string) int {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	n := 0
	for sc.Scan() {
		n++
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan %s: %v", path, err)
	}
	return n
}

// TestDomainFilenames enforces that every Go source and test file is named
// after its domain responsibility rather than a sprint, phase, milestone, or wave.
func TestDomainFilenames(t *testing.T) {
	scanned := 0
	walkRepoGoFiles(t, true, func(rel string) {
		scanned++
		base := filepath.Base(rel)
		if forbiddenFilenameRe.MatchString(base) {
			t.Errorf("%s: filename %q uses a forbidden sprint/phase/wave/milestone name; rename after its domain responsibility", rel, base)
		}
	})
	if scanned == 0 {
		t.Fatal("no Go files scanned")
	}
}

// TestNoGodFiles enforces a strict < 1,000-line ceiling on every non-test Go
// file in app/ and views/ (zero exceptions) and tight per-file ratchets on the
// few existing non-UI files above 1,000 lines.
func TestNoGodFiles(t *testing.T) {
	const defaultLimit = 1000
	// Tight per-file ratchets (+5 lines max) ONLY for pre-existing non-UI files.
	// Zero exceptions are permitted in app/ or views/.
	legacyCeilings := map[string]int{
		"data/store.go":         1568,
		"mcpui/datatools.go":    1201,
		"mcpui/mcpui.go":        1135,
		"agenthook/adapters.go": 1073,
		"cmd/mtm/main.go":       1053,
	}
	for rel := range legacyCeilings {
		if strings.HasPrefix(rel, "app/") || strings.HasPrefix(rel, "views/") {
			t.Fatalf("legacyCeilings must not exempt UI file %q", rel)
		}
	}

	scanned := 0
	walkRepoGoFiles(t, false, func(rel string) {
		scanned++
		lines := countLines(t, rel)
		if limit, ok := legacyCeilings[rel]; ok {
			if lines > limit {
				t.Errorf("%s has %d lines (ratchet ceiling %d); split it instead of growing a large file", rel, lines, limit)
			}
			return
		}
		if lines >= defaultLimit {
			t.Errorf("%s has %d lines (must be strictly < %d lines)", rel, lines, defaultLimit)
		}
	})
	if scanned == 0 {
		t.Fatal("no non-test Go files scanned")
	}
}

var modalIdentRe = regexp.MustCompile(`^modal[A-Z]`)

func isKindSelector(expr ast.Expr) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "Kind"
}

func isModalIDExpr(expr ast.Expr) bool {
	switch x := expr.(type) {
	case *ast.Ident:
		low := strings.ToLower(x.Name)
		return low == "modalid" || low == "modal"
	case *ast.CallExpr:
		if sel, ok := x.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Modal" {
			return true
		}
	case *ast.SelectorExpr:
		if x.Sel.Name == "ID" {
			if id, ok := x.X.(*ast.Ident); ok && (id.Name == "m" || strings.Contains(strings.ToLower(id.Name), "modal")) {
				return true
			}
		}
	}
	return false
}

// TestViewLayerPurity enforces that app/view.go stays free of modal switches,
// modal* identifiers, and per-noun conditionals, and that views/*.go stays free
// of .Kind switches and per-noun data.Kind* references outside dedicated widget helpers.
func TestViewLayerPurity(t *testing.T) {
	fset := token.NewFileSet()

	// 1. Check app/view.go.
	viewFile, err := parser.ParseFile(fset, "app/view.go", nil, 0)
	if err != nil {
		t.Fatalf("parse app/view.go: %v", err)
	}
	viewAllowed := map[string]map[string]bool{
		"panesSpace": {"KindPane": true, "KindPage": true},
		"termLines":  {"KindPane": true},
	}
	for _, decl := range viewFile.Decls {
		fnName := ""
		if fd, ok := decl.(*ast.FuncDecl); ok {
			fnName = fd.Name.Name
		}
		ast.Inspect(decl, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.Ident:
				if modalIdentRe.MatchString(x.Name) {
					pos := fset.Position(x.Pos())
					t.Errorf("app/view.go:%d: forbidden modal identifier %q; render modals through modal.Stack", pos.Line, x.Name)
				}
			case *ast.SwitchStmt:
				if x.Tag != nil {
					if isModalIDExpr(x.Tag) {
						pos := fset.Position(x.Pos())
						t.Errorf("app/view.go:%d: forbidden switch over modal ID", pos.Line)
					}
					if isKindSelector(x.Tag) {
						pos := fset.Position(x.Pos())
						t.Errorf("app/view.go:%d: forbidden switch over .Kind", pos.Line)
					}
				}
			case *ast.SelectorExpr:
				if id, ok := x.X.(*ast.Ident); ok && id.Name == "data" && strings.HasPrefix(x.Sel.Name, "Kind") {
					if !viewAllowed[fnName][x.Sel.Name] {
						pos := fset.Position(x.Pos())
						t.Errorf("app/view.go:%d (%s): forbidden data.%s reference outside chrome allowlist", pos.Line, fnName, x.Sel.Name)
					}
				}
			}
			return true
		})
	}

	// 2. Check views/*.go (non-test).
	entries, err := os.ReadDir("views")
	if err != nil {
		t.Fatalf("read views/: %v", err)
	}
	// Dedicated single-widget helpers may reference their own widget noun;
	// generic presenters (views.go, show.go, style.go, tree.go, outline.go, and
	// any future file) must have ZERO data.Kind* references.
	viewsWidgetAllow := map[string]map[string]bool{
		"lane.go": {"KindLane": true, "KindNote": true},
		"site.go": {"KindPane": true, "KindPage": true},
	}
	scanned := 0
	for _, ent := range entries {
		name := ent.Name()
		if ent.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		scanned++
		path := filepath.Join("views", name)
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		allowed := viewsWidgetAllow[name]
		ast.Inspect(f, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.SwitchStmt:
				if x.Tag != nil && isKindSelector(x.Tag) {
					pos := fset.Position(x.Pos())
					t.Errorf("%s:%d: forbidden switch over .Kind in views/", path, pos.Line)
				}
			case *ast.SelectorExpr:
				if id, ok := x.X.(*ast.Ident); ok && id.Name == "data" && strings.HasPrefix(x.Sel.Name, "Kind") {
					if !allowed[x.Sel.Name] {
						pos := fset.Position(x.Pos())
						t.Errorf("%s:%d: forbidden data.%s reference in views/", path, pos.Line, x.Sel.Name)
					}
				}
			}
			return true
		})
	}
	if scanned == 0 {
		t.Fatal("no non-test Go files scanned in views/")
	}
}
