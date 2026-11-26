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
	// Shrink-only per-file ratchets ONLY for pre-existing non-UI files: each is
	// the file's size when it was last tightened. Zero exceptions are permitted
	// in app/ or views/, and a file back under the limit loses its entry.
	legacyCeilings := map[string]int{
		"data/store.go":         1546,
		"mcpui/datatools.go":    1195,
		"mcpui/mcpui.go":        1130,
		"agenthook/adapters.go": 1032,
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
			if lines < defaultLimit {
				t.Errorf("%s has %d lines, under the %d-line limit: remove its stale legacy ceiling", rel, lines, defaultLimit)
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

// nonTestGoFiles lists the non-test Go files directly in dir, as repo-relative
// slash paths.
func nonTestGoFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s/: %v", dir, err)
	}
	var out []string
	for _, ent := range entries {
		name := ent.Name()
		if ent.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		out = append(out, dir+"/"+name)
	}
	if len(out) == 0 {
		t.Fatalf("no non-test Go files scanned in %s/", dir)
	}
	return out
}

func parseGoFile(t *testing.T, fset *token.FileSet, path string) *ast.File {
	t.Helper()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return f
}

// dataSelector returns the name selected from the data package ("Store" for
// data.Store), or "".
func dataSelector(expr ast.Expr) string {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	if id, ok := sel.X.(*ast.Ident); ok && id.Name == "data" {
		return sel.Sel.Name
	}
	return ""
}

// mentionsStore reports whether a field list of the data package names the
// Store type.
func mentionsStore(fields *ast.FieldList) bool {
	found := false
	if fields == nil {
		return false
	}
	ast.Inspect(fields, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && id.Name == "Store" {
			found = true
		}
		return !found
	})
	return found
}

// dataStoreFuncs are the data package's exported functions that take or
// return a Store: calling one is a way to hold a store.
func dataStoreFuncs(t *testing.T) map[string]bool {
	t.Helper()
	fset := token.NewFileSet()
	out := map[string]bool{}
	for _, path := range nonTestGoFiles(t, "data") {
		for _, decl := range parseGoFile(t, fset, path).Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Recv != nil || !fd.Name.IsExported() {
				continue
			}
			if mentionsStore(fd.Type.Params) || mentionsStore(fd.Type.Results) {
				out[fd.Name.Name] = true
			}
		}
	}
	if !out["Open"] {
		t.Fatal("data.Open was not found among the data functions that return a Store")
	}
	return out
}

// TestViewsNoStore enforces that presenters draw from preloaded value
// objects: no non-test file in views/ may name data.Store (in a parameter, a
// struct field, a variable or a conversion), call a data function that takes
// or returns one, or read a Store field off another value. The ceiling is
// zero; load a snapshot in data/ and pass it in.
func TestViewsNoStore(t *testing.T) {
	storeFuncs := dataStoreFuncs(t)
	fset := token.NewFileSet()
	for _, path := range nonTestGoFiles(t, "views") {
		f := parseGoFile(t, fset, path)
		called := map[*ast.SelectorExpr]bool{}
		ast.Inspect(f, func(n ast.Node) bool {
			line := func() int { return fset.Position(n.Pos()).Line }
			switch x := n.(type) {
			case *ast.CallExpr:
				if sel, ok := x.Fun.(*ast.SelectorExpr); ok {
					called[sel] = true
				}
			case *ast.Field:
				for _, name := range x.Names {
					if name.Name == "Store" {
						t.Errorf("%s:%d: forbidden Store field in views/", path, line())
					}
				}
			case *ast.SelectorExpr:
				switch name := dataSelector(x); {
				case name == "Store":
					t.Errorf("%s:%d: forbidden data.Store reference in views/", path, line())
				case storeFuncs[name]:
					t.Errorf("%s:%d: forbidden data.%s (it takes or returns a store) in views/", path, line(), name)
				case name == "" && x.Sel.Name == "Store" && !called[x]:
					t.Errorf("%s:%d: forbidden read of a .Store field in views/", path, line())
				}
			}
			return true
		})
	}
}

func isDataKind(expr ast.Expr) bool {
	return strings.HasPrefix(dataSelector(expr), "Kind")
}

func isModalCall(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok || len(call.Args) != 0 {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "Modal"
}

// identityChecks counts the special-case conditionals of a file: comparisons
// (== or !=) and switch cases against a data.Kind* noun, and comparisons and
// switch cases on which modal is open (x.Modal()).
func identityChecks(f *ast.File) int {
	n := 0
	ast.Inspect(f, func(node ast.Node) bool {
		switch x := node.(type) {
		case *ast.BinaryExpr:
			if x.Op != token.EQL && x.Op != token.NEQ {
				return true
			}
			if isDataKind(x.X) || isDataKind(x.Y) {
				n++
			}
			if isModalCall(x.X) || isModalCall(x.Y) {
				n++
			}
		case *ast.SwitchStmt:
			onModal := x.Tag != nil && isModalCall(x.Tag)
			for _, stmt := range x.Body.List {
				for _, expr := range stmt.(*ast.CaseClause).List {
					if isDataKind(expr) || onModal {
						n++
					}
				}
			}
		}
		return true
	})
	return n
}

// specialCaseCeilings are the identity checks each existing app/ file still
// holds. They only shrink: a file may not exceed its ceiling, and a file that
// drops below it must have the ceiling lowered to match (or its entry removed
// at zero). A file not listed, every views/ file, app/view.go and
// app/modals.go have a ceiling of zero.
var specialCaseCeilings = map[string]int{
	"app/app.go":                    2,
	"app/canvas_links.go":           1,
	"app/chips.go":                  1,
	"app/cmdline.go":                1,
	"app/commands_run.go":           2,
	"app/context_cache.go":          3,
	"app/context_delta.go":          2,
	"app/context_index.go":          2,
	"app/drafts.go":                 1,
	"app/ext_actions.go":            1,
	"app/ext_catalog.go":            1,
	"app/ext_install.go":            3,
	"app/features_capture.go":       4,
	"app/features_itemized.go":      1,
	"app/filter.go":                 1,
	"app/focus_guard.go":            1,
	"app/git_hub.go":                2,
	"app/insert_picker.go":          3,
	"app/inspect.go":                1,
	"app/live.go":                   2,
	"app/nav.go":                    2,
	"app/open.go":                   5,
	"app/outline_hoist.go":          3,
	"app/outline_modal.go":          11,
	"app/outline_triage.go":         3,
	"app/page_target.go":            2,
	"app/panes_associate.go":        2,
	"app/panes_composer.go":         3,
	"app/panes_questions.go":        5,
	"app/panes_rail.go":             6,
	"app/panes_side.go":             2,
	"app/panes_terminal.go":         2,
	"app/project_home.go":           6,
	"app/project_home_dashboard.go": 8,
	"app/project_nav_preselect.go":  1,
	"app/project_nav_preview.go":    10,
	"app/project_nav_tree.go":       5,
	"app/project_nav_unread.go":     17,
	"app/project_outline.go":        1,
	"app/project_portal.go":         2,
	"app/project_sections.go":       2,
	"app/project_tabs.go":           3,
	"app/record_actions.go":         2,
	"app/records.go":                17,
	"app/registers.go":              2,
	"app/rich_composer.go":          1,
	"app/runtime_daemon.go":         1,
	"app/runtime_harnesses.go":      5,
	"app/schedules.go":              2,
	"app/selection.go":              7,
	"app/sessions_desk.go":          2,
	"app/smoke.go":                  51,
	"app/table_presets.go":          1,
	"app/tables.go":                 1,
	"app/task_lineage.go":           4,
	"app/task_threads.go":           9,
	"app/test_summary.go":           1,
	"app/tree_controller.go":        11,
	"app/tutorial.go":               3,
	"app/ux_notes.go":               1,
	"app/v2.go":                     9,
	"app/view_cycle.go":             2,
	"app/zero_field.go":             2,
}

// TestNoSpecialCaseConditionals is the shrink-only ratchet on per-noun
// (data.Kind*) and per-modal (x.Modal() == "...") branches in the generic
// layers. Declare the behaviour as data (a schema property, a binding guard
// over a fact) instead of adding a branch.
func TestNoSpecialCaseConditionals(t *testing.T) {
	for file := range specialCaseCeilings {
		if strings.HasPrefix(file, "views/") || file == "app/view.go" || file == "app/modals.go" {
			t.Fatalf("specialCaseCeilings must not exempt generic file %q", file)
		}
	}
	fset := token.NewFileSet()
	seen := map[string]bool{}
	for _, dir := range []string{"views", "app"} {
		for _, path := range nonTestGoFiles(t, dir) {
			seen[path] = true
			count, ceiling := identityChecks(parseGoFile(t, fset, path)), specialCaseCeilings[path]
			switch {
			case count > ceiling:
				t.Errorf("%s: %d data.Kind*/Modal() identity checks (ceiling %d); express the case as data instead of a branch", path, count, ceiling)
			case count < ceiling:
				t.Errorf("%s: %d data.Kind*/Modal() identity checks, under its ceiling of %d: lower specialCaseCeilings to %d", path, count, ceiling, count)
			}
		}
	}
	for file := range specialCaseCeilings {
		if !seen[file] {
			t.Errorf("specialCaseCeilings lists %s, which is gone: remove its entry", file)
		}
	}
}
