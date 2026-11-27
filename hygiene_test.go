package matomeru_deps_test

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

const (
	ruleIdentityKindModal   = "identity.kind_modal"
	ruleIdentityActivePage  = "identity.active_page"
	ruleIdentityFocusRegion = "identity.focus_region_id"
	ruleReuseCustomFilter   = "reuse.custom_filter"
	ruleReuseRawNounFormat  = "reuse.raw_noun_format"
	ruleReuseModalChrome    = "reuse.custom_modal_chrome"
	ruleComplexityCyclo     = "complexity.cyclomatic"

	categoryIdentityDispatch = "Identity Dispatch"
	categoryParallelReimpl   = "Parallel Reimplementation"
	categoryCyclomatic       = "Cyclomatic Complexity"

	reusableKindModal   = "Schema properties, data.Presentations, modal.Stack, or binding when-guards over Facts"
	reusableActivePage  = "ViewNode.Filter, traits.* scopes, Facts, or declarative page/view metadata"
	reusableFocusRegion = "ViewNode.ByID, ViewNode.Filter, ViewNode.Traits, Facts, or ochazuke focus/layout engine"
	reusableFilter      = "traits.Filterable + ViewNode.Filter + traits.CompileMultiTerm"
	reusableRawNoun     = "data.Presentations (chip/inline/row/card/detail), core.Ref, or typed composite keys"
	reusableModalChrome = "ochazuke/widgets/modal.Frame (BuildOverlay / FitHeightOverlay) + modal.Stack"
	reusableCyclomatic  = "Decompose into pure data tables, declarative bindings/traits, or small cohesive helpers (cap <= 12)"

	maxCyclomaticComplexity = 12
)

type hygieneFinding struct {
	Rule     string `json:"rule"`
	Category string `json:"category"`
	File     string `json:"file"`
	Line     int    `json:"line"`
	Func     string `json:"func"`
	Detail   string `json:"detail"`
	Snippet  string `json:"snippet"`
	Reusable string `json:"reusable"`
	Severity string `json:"severity"` // "ratcheted" or "blocked"
	Value    int    `json:"value,omitempty"`
	Cap      int    `json:"cap,omitempty"`
}

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

func readSourceLines(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return strings.Split(string(b), "\n")
}

func lineSnippet(lines []string, line int) string {
	if line < 1 || line > len(lines) {
		return ""
	}
	s := strings.TrimSpace(lines[line-1])
	rs := []rune(s)
	if len(rs) > 120 {
		return string(rs[:117]) + "..."
	}
	return s
}

func recvTypeName(expr ast.Expr) string {
	switch x := expr.(type) {
	case *ast.StarExpr:
		return recvTypeName(x.X)
	case *ast.Ident:
		return x.Name
	case *ast.IndexExpr:
		return recvTypeName(x.X)
	case *ast.IndexListExpr:
		return recvTypeName(x.X)
	case *ast.SelectorExpr:
		return x.Sel.Name
	default:
		return "?"
	}
}

func funcDeclName(fd *ast.FuncDecl) string {
	if fd == nil {
		return ""
	}
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return fd.Name.Name
	}
	return fmt.Sprintf("(%s).%s", recvTypeName(fd.Recv.List[0].Type), fd.Name.Name)
}

func declContextName(decl ast.Decl) string {
	if fd, ok := decl.(*ast.FuncDecl); ok {
		return funcDeclName(fd)
	}
	return ""
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
	for rel := range legacyCeilings {
		if strings.HasPrefix(rel, "app/") || strings.HasPrefix(rel, "views/") {
			t.Fatalf("legacyCeilings must not exempt UI file %q", rel)
		}
	}

	seen := map[string]bool{}
	scanned := 0
	walkRepoGoFiles(t, false, func(rel string) {
		scanned++
		seen[rel] = true
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
	for rel := range legacyCeilings {
		if !seen[rel] {
			t.Errorf("legacyCeilings lists %s, which is gone: remove its entry", rel)
		}
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
	sort.Strings(out)
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

// identityChecks returns detailed findings for special-case conditionals in a
// file: comparisons (== or !=) and switch cases against a data.Kind* noun, and
// comparisons and switch cases on which modal is open (x.Modal()).
func identityChecks(fset *token.FileSet, path string, f *ast.File, lines []string) []hygieneFinding {
	var out []hygieneFinding
	add := func(pos token.Pos, fnName, detail string) {
		ln := fset.Position(pos).Line
		out = append(out, hygieneFinding{
			Rule:     ruleIdentityKindModal,
			Category: categoryIdentityDispatch,
			File:     path,
			Line:     ln,
			Func:     fnName,
			Detail:   detail,
			Snippet:  lineSnippet(lines, ln),
			Reusable: reusableKindModal,
		})
	}
	for _, decl := range f.Decls {
		fnName := declContextName(decl)
		ast.Inspect(decl, func(node ast.Node) bool {
			switch x := node.(type) {
			case *ast.BinaryExpr:
				if x.Op != token.EQL && x.Op != token.NEQ {
					return true
				}
				if isDataKind(x.X) || isDataKind(x.Y) {
					kindName := dataSelector(x.X)
					if !strings.HasPrefix(kindName, "Kind") {
						kindName = dataSelector(x.Y)
					}
					add(x.Pos(), fnName, fmt.Sprintf("comparison against data.%s", kindName))
				}
				if isModalCall(x.X) || isModalCall(x.Y) {
					add(x.Pos(), fnName, "comparison against Modal()")
				}
			case *ast.SwitchStmt:
				onModal := x.Tag != nil && isModalCall(x.Tag)
				for _, stmt := range x.Body.List {
					cc := stmt.(*ast.CaseClause)
					for _, expr := range cc.List {
						if isDataKind(expr) {
							add(expr.Pos(), fnName, fmt.Sprintf("switch case data.%s", dataSelector(expr)))
						} else if onModal {
							add(expr.Pos(), fnName, "switch case on Modal()")
						}
					}
				}
			}
			return true
		})
	}
	return out
}

func scanIdentityKindModal(t *testing.T) []hygieneFinding {
	t.Helper()
	fset := token.NewFileSet()
	var all []hygieneFinding
	for _, dir := range []string{"views", "app"} {
		for _, path := range nonTestGoFiles(t, dir) {
			f := parseGoFile(t, fset, path)
			lines := readSourceLines(t, path)
			all = append(all, identityChecks(fset, path, f, lines)...)
		}
	}
	return annotateFileSeverity(all, specialCaseCeilings)
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
	assertFileRatchet(
		t,
		[]string{"views", "app"},
		scanIdentityKindModal(t),
		specialCaseCeilings,
		"specialCaseCeilings",
		"data.Kind*/Modal() identity checks; express the case as data instead of a branch",
	)
}

func isActivePageSelector(expr ast.Expr) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "ActivePage"
}

func isSpaceConstName(name string) bool {
	return strings.HasPrefix(name, "Space") && len(name) > len("Space") && unicode.IsUpper(rune(name[len("Space")]))
}

func spaceIdentName(expr ast.Expr) string {
	switch x := expr.(type) {
	case *ast.Ident:
		if isSpaceConstName(x.Name) {
			return x.Name
		}
	case *ast.SelectorExpr:
		if isSpaceConstName(x.Sel.Name) {
			return x.Sel.Name
		}
	}
	return ""
}

func activePageChecks(fset *token.FileSet, path string, f *ast.File, lines []string) []hygieneFinding {
	var out []hygieneFinding
	add := func(pos token.Pos, fnName, detail string) {
		ln := fset.Position(pos).Line
		out = append(out, hygieneFinding{
			Rule:     ruleIdentityActivePage,
			Category: categoryIdentityDispatch,
			File:     path,
			Line:     ln,
			Func:     fnName,
			Detail:   detail,
			Snippet:  lineSnippet(lines, ln),
			Reusable: reusableActivePage,
		})
	}
	for _, decl := range f.Decls {
		fnName := declContextName(decl)
		ast.Inspect(decl, func(node ast.Node) bool {
			switch x := node.(type) {
			case *ast.BinaryExpr:
				if x.Op != token.EQL && x.Op != token.NEQ {
					return true
				}
				ap := isActivePageSelector(x.X) || isActivePageSelector(x.Y)
				sp := spaceIdentName(x.X)
				if sp == "" {
					sp = spaceIdentName(x.Y)
				}
				switch {
				case ap && sp != "":
					add(x.Pos(), fnName, fmt.Sprintf("ActivePage comparison against %s", sp))
				case ap:
					add(x.Pos(), fnName, "comparison on .ActivePage")
				case sp != "":
					add(x.Pos(), fnName, fmt.Sprintf("comparison against %s", sp))
				}
			case *ast.SwitchStmt:
				onActivePage := x.Tag != nil && isActivePageSelector(x.Tag)
				for _, stmt := range x.Body.List {
					cc := stmt.(*ast.CaseClause)
					if len(cc.List) == 0 {
						continue
					}
					if onActivePage {
						add(cc.Pos(), fnName, "switch case on .ActivePage")
						continue
					}
					for _, expr := range cc.List {
						if sp := spaceIdentName(expr); sp != "" {
							add(expr.Pos(), fnName, fmt.Sprintf("switch case %s", sp))
						}
					}
				}
			}
			return true
		})
	}
	return out
}

func scanActivePageDispatch(t *testing.T) []hygieneFinding {
	t.Helper()
	fset := token.NewFileSet()
	var all []hygieneFinding
	for _, dir := range []string{"views", "app"} {
		for _, path := range nonTestGoFiles(t, dir) {
			f := parseGoFile(t, fset, path)
			lines := readSourceLines(t, path)
			all = append(all, activePageChecks(fset, path, f, lines)...)
		}
	}
	return annotateFileSeverity(all, activePageCeilings)
}

// TestNoActivePageDispatch enforces a strict zero ceiling in views/ and all new
// app/ files, and a shrink-only ratchet on existing app/ files for .ActivePage
// and Space* dispatch branches.
func TestNoActivePageDispatch(t *testing.T) {
	for file := range activePageCeilings {
		if strings.HasPrefix(file, "views/") {
			t.Fatalf("activePageCeilings must not exempt views/ file %q", file)
		}
	}
	assertFileRatchet(
		t,
		[]string{"views", "app"},
		scanActivePageDispatch(t),
		activePageCeilings,
		"activePageCeilings",
		".ActivePage/Space* dispatch checks; use ViewNode.Filter, traits.* scopes, Facts, or declarative page/view metadata",
	)
}

var focusRegionIDNames = map[string]bool{
	"FocusID":  true,
	"focusID":  true,
	"Focus":    true,
	"region":   true,
	"Region":   true,
	"targetID": true,
	"TargetID": true,
	"ownerID":  true,
	"listID":   true,
}

var hardcodedContainerIDs = map[string]bool{
	"project.nav":     true,
	"site.toc":        true,
	"work.table":      true,
	"outline.tree":    true,
	"project.outline": true,
	"project.panes":   true,
	"project.lanes":   true,
}

func focusOrRegionName(expr ast.Expr) string {
	switch x := expr.(type) {
	case *ast.Ident:
		if focusRegionIDNames[x.Name] {
			return x.Name
		}
	case *ast.SelectorExpr:
		if focusRegionIDNames[x.Sel.Name] {
			return x.Sel.Name
		}
	case *ast.CallExpr:
		if len(x.Args) != 0 {
			return ""
		}
		switch fn := x.Fun.(type) {
		case *ast.Ident:
			if focusRegionIDNames[fn.Name] {
				return fn.Name + "()"
			}
		case *ast.SelectorExpr:
			if focusRegionIDNames[fn.Sel.Name] {
				return fn.Sel.Name + "()"
			}
		}
	}
	return ""
}

func nonEmptyStringLit(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	val, err := strconv.Unquote(lit.Value)
	if err != nil || val == "" {
		return "", false
	}
	return val, true
}

func isHardcodedContainerLit(expr ast.Expr, allowDottedPrefix bool) (string, bool) {
	val, ok := nonEmptyStringLit(expr)
	if !ok {
		return "", false
	}
	if hardcodedContainerIDs[val] {
		return val, true
	}
	if allowDottedPrefix && hardcodedContainerIDs[strings.TrimSuffix(val, ".")] {
		return val, true
	}
	return "", false
}

func focusRegionIDChecks(fset *token.FileSet, path string, f *ast.File, lines []string) []hygieneFinding {
	var out []hygieneFinding
	add := func(pos token.Pos, fnName, detail string) {
		ln := fset.Position(pos).Line
		out = append(out, hygieneFinding{
			Rule:     ruleIdentityFocusRegion,
			Category: categoryIdentityDispatch,
			File:     path,
			Line:     ln,
			Func:     fnName,
			Detail:   detail,
			Snippet:  lineSnippet(lines, ln),
			Reusable: reusableFocusRegion,
		})
	}
	for _, decl := range f.Decls {
		fnName := declContextName(decl)
		ast.Inspect(decl, func(node ast.Node) bool {
			switch x := node.(type) {
			case *ast.BinaryExpr:
				if x.Op != token.EQL && x.Op != token.NEQ {
					return true
				}
				if name := focusOrRegionName(x.X); name != "" {
					if val, ok := nonEmptyStringLit(x.Y); ok {
						add(x.Pos(), fnName, fmt.Sprintf("%s compared against %q", name, val))
						return true
					}
				}
				if name := focusOrRegionName(x.Y); name != "" {
					if val, ok := nonEmptyStringLit(x.X); ok {
						add(x.Pos(), fnName, fmt.Sprintf("%s compared against %q", name, val))
						return true
					}
				}
				if val, ok := isHardcodedContainerLit(x.X, false); ok {
					add(x.Pos(), fnName, fmt.Sprintf("comparison against container ID %q", val))
				} else if val, ok := isHardcodedContainerLit(x.Y, false); ok {
					add(x.Pos(), fnName, fmt.Sprintf("comparison against container ID %q", val))
				}
			case *ast.SwitchStmt:
				onFocus := ""
				if x.Tag != nil {
					onFocus = focusOrRegionName(x.Tag)
				}
				for _, stmt := range x.Body.List {
					cc := stmt.(*ast.CaseClause)
					for _, expr := range cc.List {
						if onFocus != "" {
							if val, ok := nonEmptyStringLit(expr); ok {
								add(expr.Pos(), fnName, fmt.Sprintf("switch case %q on %s", val, onFocus))
								continue
							}
						}
						if val, ok := isHardcodedContainerLit(expr, false); ok {
							add(expr.Pos(), fnName, fmt.Sprintf("switch case container ID %q", val))
						}
					}
				}
			case *ast.CallExpr:
				sel, ok := x.Fun.(*ast.SelectorExpr)
				if !ok || len(x.Args) != 2 {
					return true
				}
				pkg, ok := sel.X.(*ast.Ident)
				if !ok || pkg.Name != "strings" {
					return true
				}
				switch sel.Sel.Name {
				case "HasPrefix", "HasSuffix", "Contains":
					if name := focusOrRegionName(x.Args[0]); name != "" {
						if val, ok := nonEmptyStringLit(x.Args[1]); ok {
							add(x.Pos(), fnName, fmt.Sprintf("strings.%s(%s, %q)", sel.Sel.Name, name, val))
							return true
						}
					}
					if val, ok := isHardcodedContainerLit(x.Args[1], true); ok {
						add(x.Pos(), fnName, fmt.Sprintf("strings.%s against container ID %q", sel.Sel.Name, val))
					}
				}
			}
			return true
		})
	}
	return out
}

func scanFocusOrRegionIDDispatch(t *testing.T) []hygieneFinding {
	t.Helper()
	fset := token.NewFileSet()
	var all []hygieneFinding
	for _, dir := range []string{"views", "app"} {
		for _, path := range nonTestGoFiles(t, dir) {
			f := parseGoFile(t, fset, path)
			lines := readSourceLines(t, path)
			all = append(all, focusRegionIDChecks(fset, path, f, lines)...)
		}
	}
	return annotateFileSeverity(all, focusRegionIDCeilings)
}

// TestNoFocusOrRegionIDDispatch enforces a strict zero ceiling in views/ and
// all new app/ files, and a shrink-only ratchet on existing app/ files for
// hardcoded focus/region/container string-ID dispatch.
func TestNoFocusOrRegionIDDispatch(t *testing.T) {
	for file := range focusRegionIDCeilings {
		if strings.HasPrefix(file, "views/") {
			t.Fatalf("focusRegionIDCeilings must not exempt views/ file %q", file)
		}
	}
	assertFileRatchet(
		t,
		[]string{"views", "app"},
		scanFocusOrRegionIDDispatch(t),
		focusRegionIDCeilings,
		"focusRegionIDCeilings",
		"focus/region/container string-ID dispatch checks; use ViewNode.ByID, ViewNode.Filter, ViewNode.Traits, Facts, or ochazuke focus/layout engine",
	)
}

var canonicalFilterFields = map[string]bool{
	"Filter":       true,
	"FilterField":  true,
	"FilterSpec":   true,
	"MatchMode":    true,
	"filters":      true,
	"filterTarget": true,
	"filterSpec":   true,
	"filterModes":  true,
}

func isAdhocFilterFieldName(name string) bool {
	if canonicalFilterFields[name] {
		return false
	}
	return strings.HasSuffix(name, "Filter") ||
		strings.HasSuffix(name, "Filters") ||
		strings.HasSuffix(name, "MatchMode") ||
		strings.HasSuffix(name, "MatchModes")
}

func exprContainsStringsToLower(expr ast.Expr) bool {
	found := false
	ast.Inspect(expr, func(n ast.Node) bool {
		if found {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
			if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "strings" && sel.Sel.Name == "ToLower" {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

func stmtHasFilterRegistration(node ast.Node) bool {
	found := false
	ast.Inspect(node, func(n ast.Node) bool {
		if found {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
			name := sel.Sel.Name
			if strings.HasPrefix(name, "register") || name == "Register" || name == "Unregister" {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

func parallelFilterChecks(fset *token.FileSet, path string, f *ast.File, lines []string) []hygieneFinding {
	var out []hygieneFinding
	add := func(pos token.Pos, fnName, detail string) {
		ln := fset.Position(pos).Line
		out = append(out, hygieneFinding{
			Rule:     ruleReuseCustomFilter,
			Category: categoryParallelReimpl,
			File:     path,
			Line:     ln,
			Func:     fnName,
			Detail:   detail,
			Snippet:  lineSnippet(lines, ln),
			Reusable: reusableFilter,
		})
	}
	for _, decl := range f.Decls {
		// (a) Ad-hoc struct fields ending in Filter / Filters / MatchMode(s).
		if gd, ok := decl.(*ast.GenDecl); ok && gd.Tok == token.TYPE {
			for _, spec := range gd.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok || st.Fields == nil {
					continue
				}
				for _, field := range st.Fields.List {
					for _, ident := range field.Names {
						if isAdhocFilterFieldName(ident.Name) {
							add(ident.Pos(), "type "+ts.Name.Name, fmt.Sprintf("ad-hoc filter field %s.%s", ts.Name.Name, ident.Name))
						}
					}
				}
			}
		}

		fnName := declContextName(decl)
		if fd, ok := decl.(*ast.FuncDecl); ok {
			// (c) Whitelist registration functions syncFilterRegistry and filterTable.
			if fd.Name.Name == "filterTable" {
				add(fd.Pos(), fnName, "ad-hoc filterTable whitelist resolver")
			}
			if fd.Name.Name == "syncFilterRegistry" && fd.Body != nil {
				ast.Inspect(fd.Body, func(n ast.Node) bool {
					switch stmt := n.(type) {
					case *ast.IfStmt:
						if stmtHasFilterRegistration(stmt) {
							add(stmt.Pos(), fnName, "syncFilterRegistry whitelist if-branch")
						}
					case *ast.ForStmt:
						if stmtHasFilterRegistration(stmt) {
							add(stmt.Pos(), fnName, "syncFilterRegistry whitelist for-branch")
						}
					case *ast.RangeStmt:
						if stmtHasFilterRegistration(stmt) {
							add(stmt.Pos(), fnName, "syncFilterRegistry whitelist range-branch")
						}
					}
					return true
				})
			}
		}

		// (b) Ad-hoc case-insensitive substring matching or regexp.Compile.
		ast.Inspect(decl, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			if pkg.Name == "strings" && sel.Sel.Name == "Contains" && len(call.Args) == 2 {
				if exprContainsStringsToLower(call.Args[0]) || exprContainsStringsToLower(call.Args[1]) {
					add(call.Pos(), fnName, "ad-hoc case-insensitive strings.Contains(strings.ToLower(...))")
				}
			}
			if pkg.Name == "regexp" && sel.Sel.Name == "Compile" {
				add(call.Pos(), fnName, "ad-hoc regexp.Compile in UI layer")
			}
			return true
		})
	}
	return out
}

func scanParallelFilterReimpl(t *testing.T) []hygieneFinding {
	t.Helper()
	fset := token.NewFileSet()
	var all []hygieneFinding
	for _, dir := range []string{"views", "app"} {
		for _, path := range nonTestGoFiles(t, dir) {
			f := parseGoFile(t, fset, path)
			lines := readSourceLines(t, path)
			all = append(all, parallelFilterChecks(fset, path, f, lines)...)
		}
	}
	return annotateFileSeverity(all, parallelFilterCeilings)
}

// TestNoParallelFilterReimpl catches ad-hoc filter state, case-insensitive
// substring matching, regexp.Compile, and whitelist registration helpers in
// app/ and views/.
func TestNoParallelFilterReimpl(t *testing.T) {
	assertFileRatchet(
		t,
		[]string{"views", "app"},
		scanParallelFilterReimpl(t),
		parallelFilterCeilings,
		"parallelFilterCeilings",
		"parallel filter reimplementations; use traits.Filterable + ViewNode.Filter + traits.CompileMultiTerm",
	)
}

var rawNounPrefixes = []string{
	"task:", "lane:", "project:", "pane:", "note:", "doc:", "document:",
	"question:", "page:", "screen:", "noun:", "section:", "thread:",
	"queue:", "command:", "context:", "worktree:",
}

func isWordChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_'
}

func matchRawNounPrefix(val string, anywhere bool) (string, bool) {
	for _, prefix := range rawNounPrefixes {
		if strings.HasPrefix(val, prefix) {
			return prefix, true
		}
		if anywhere {
			search := val
			offset := 0
			for {
				idx := strings.Index(search, prefix)
				if idx < 0 {
					break
				}
				absIdx := offset + idx
				if absIdx == 0 || !isWordChar(val[absIdx-1]) {
					return prefix, true
				}
				offset = absIdx + len(prefix)
				search = val[offset:]
			}
		}
	}
	return "", false
}

func rawNounPrefixLit(expr ast.Expr, anywhere bool) (string, bool) {
	val, ok := nonEmptyStringLit(expr)
	if !ok {
		return "", false
	}
	return matchRawNounPrefix(val, anywhere)
}

func rawNounFormatChecks(fset *token.FileSet, path string, f *ast.File, lines []string) []hygieneFinding {
	var out []hygieneFinding
	add := func(pos token.Pos, fnName, detail string) {
		ln := fset.Position(pos).Line
		out = append(out, hygieneFinding{
			Rule:     ruleReuseRawNounFormat,
			Category: categoryParallelReimpl,
			File:     path,
			Line:     ln,
			Func:     fnName,
			Detail:   detail,
			Snippet:  lineSnippet(lines, ln),
			Reusable: reusableRawNoun,
		})
	}
	for _, decl := range f.Decls {
		fnName := declContextName(decl)
		ast.Inspect(decl, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.BinaryExpr:
				if x.Op != token.ADD && x.Op != token.EQL && x.Op != token.NEQ {
					return true
				}
				if p, ok := rawNounPrefixLit(x.X, false); ok {
					add(x.X.Pos(), fnName, fmt.Sprintf("raw noun prefix %q in %s expression", p, x.Op))
				}
				if p, ok := rawNounPrefixLit(x.Y, false); ok {
					add(x.Y.Pos(), fnName, fmt.Sprintf("raw noun prefix %q in %s expression", p, x.Op))
				}
			case *ast.CallExpr:
				sel, ok := x.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pkg, ok := sel.X.(*ast.Ident)
				if !ok {
					return true
				}
				if pkg.Name == "strings" && (sel.Sel.Name == "HasPrefix" || sel.Sel.Name == "TrimPrefix" || sel.Sel.Name == "Split") {
					for _, arg := range x.Args {
						if p, ok := rawNounPrefixLit(arg, false); ok {
							add(arg.Pos(), fnName, fmt.Sprintf("raw noun prefix %q in strings.%s", p, sel.Sel.Name))
						}
					}
				}
				if pkg.Name == "fmt" && sel.Sel.Name == "Sprintf" && len(x.Args) > 0 {
					if p, ok := rawNounPrefixLit(x.Args[0], true); ok {
						add(x.Args[0].Pos(), fnName, fmt.Sprintf("raw noun prefix %q in fmt.Sprintf", p))
					}
					for _, arg := range x.Args[1:] {
						if p, ok := rawNounPrefixLit(arg, false); ok {
							add(arg.Pos(), fnName, fmt.Sprintf("raw noun prefix %q in fmt.Sprintf arg", p))
						}
					}
				}
			}
			return true
		})
	}
	return out
}

func scanRawNounPrefixFormatting(t *testing.T) []hygieneFinding {
	t.Helper()
	fset := token.NewFileSet()
	var all []hygieneFinding
	for _, dir := range []string{"views", "app"} {
		for _, path := range nonTestGoFiles(t, dir) {
			f := parseGoFile(t, fset, path)
			lines := readSourceLines(t, path)
			all = append(all, rawNounFormatChecks(fset, path, f, lines)...)
		}
	}
	return annotateFileSeverity(all, rawNounFormatCeilings)
}

// TestNoRawNounPrefixFormatting catches hand-rolled colon-prefixed noun and
// composite-ID strings in app/ and views/.
func TestNoRawNounPrefixFormatting(t *testing.T) {
	assertFileRatchet(
		t,
		[]string{"views", "app"},
		scanRawNounPrefixFormatting(t),
		rawNounFormatCeilings,
		"rawNounFormatCeilings",
		"raw noun/composite-ID prefix formatting; use data.Presentations, core.Ref, or typed composite keys",
	)
}

func isUIKindOverlay(expr ast.Expr) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "KindOverlay" {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "ui"
}

func parallelModalChromeChecks(fset *token.FileSet, path string, f *ast.File, lines []string) []hygieneFinding {
	var out []hygieneFinding
	add := func(pos token.Pos, fnName, detail string) {
		ln := fset.Position(pos).Line
		out = append(out, hygieneFinding{
			Rule:     ruleReuseModalChrome,
			Category: categoryParallelReimpl,
			File:     path,
			Line:     ln,
			Func:     fnName,
			Detail:   detail,
			Snippet:  lineSnippet(lines, ln),
			Reusable: reusableModalChrome,
		})
	}
	for _, decl := range f.Decls {
		fnName := declContextName(decl)
		ast.Inspect(decl, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.KeyValueExpr:
				if isUIKindOverlay(x.Value) {
					add(x.Pos(), fnName, "direct ui.KindOverlay construction")
				}
			case *ast.AssignStmt:
				for _, rhs := range x.Rhs {
					if isUIKindOverlay(rhs) {
						add(rhs.Pos(), fnName, "direct ui.KindOverlay assignment")
					}
				}
			}
			return true
		})
	}
	return out
}

func scanParallelModalChrome(t *testing.T) []hygieneFinding {
	t.Helper()
	fset := token.NewFileSet()
	var all []hygieneFinding
	for _, dir := range []string{"views", "app"} {
		for _, path := range nonTestGoFiles(t, dir) {
			f := parseGoFile(t, fset, path)
			lines := readSourceLines(t, path)
			all = append(all, parallelModalChromeChecks(fset, path, f, lines)...)
		}
	}
	return annotateFileSeverity(all, parallelModalChromeCeilings)
}

// TestNoParallelModalChrome catches direct construction of ui.KindOverlay in
// app/ and views/ instead of delegating to ochazuke/widgets/modal.Frame.
func TestNoParallelModalChrome(t *testing.T) {
	assertFileRatchet(
		t,
		[]string{"views", "app"},
		scanParallelModalChrome(t),
		parallelModalChromeCeilings,
		"parallelModalChromeCeilings",
		"direct ui.KindOverlay constructions; use ochazuke/widgets/modal.Frame (BuildOverlay / FitHeightOverlay) + modal.Stack",
	)
}

func cyclomaticComplexity(body *ast.BlockStmt) int {
	if body == nil {
		return 1
	}
	c := 1
	ast.Inspect(body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt:
			c++
		case *ast.CaseClause:
			if len(x.List) > 0 {
				c++
			}
		case *ast.CommClause:
			if x.Comm != nil {
				c++
			}
		case *ast.BinaryExpr:
			if x.Op == token.LAND || x.Op == token.LOR {
				c++
			}
		}
		return true
	})
	return c
}

func cyclomaticChecks(fset *token.FileSet, path string, f *ast.File, lines []string) []hygieneFinding {
	var out []hygieneFinding
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		c := cyclomaticComplexity(fd.Body)
		if c <= maxCyclomaticComplexity {
			continue
		}
		fnName := funcDeclName(fd)
		ln := fset.Position(fd.Pos()).Line
		out = append(out, hygieneFinding{
			Rule:     ruleComplexityCyclo,
			Category: categoryCyclomatic,
			File:     path,
			Line:     ln,
			Func:     fnName,
			Detail:   fmt.Sprintf("cyclomatic complexity %d exceeds cap %d", c, maxCyclomaticComplexity),
			Snippet:  lineSnippet(lines, ln),
			Reusable: reusableCyclomatic,
			Value:    c,
			Cap:      maxCyclomaticComplexity,
		})
	}
	return out
}

func scanCyclomaticComplexity(t *testing.T) []hygieneFinding {
	t.Helper()
	fset := token.NewFileSet()
	var all []hygieneFinding
	for _, dir := range []string{"app", "views", "data"} {
		for _, path := range nonTestGoFiles(t, dir) {
			f := parseGoFile(t, fset, path)
			lines := readSourceLines(t, path)
			all = append(all, cyclomaticChecks(fset, path, f, lines)...)
		}
	}
	for i := range all {
		key := all[i].File + ":" + all[i].Func
		if ceiling := cyclomaticCeilings[key]; ceiling > 0 && all[i].Value <= ceiling {
			all[i].Severity = "ratcheted"
		} else {
			all[i].Severity = "blocked"
		}
	}
	return all
}

// TestMaxCyclomaticComplexity enforces a strict McCabe cyclomatic complexity
// cap of <= 12 on every new function in app/, views/, and data/, with a
// two-way shrink-only per-function ratchet for existing functions above 12.
func TestMaxCyclomaticComplexity(t *testing.T) {
	findings := scanCyclomaticComplexity(t)
	seen := map[string]bool{}
	for _, f := range findings {
		key := f.File + ":" + f.Func
		seen[key] = true
		ceiling := cyclomaticCeilings[key]
		switch {
		case ceiling == 0:
			t.Errorf("%s:%d (%s): cyclomatic complexity %d exceeds cap %d; decompose into pure data tables or cohesive helpers", f.File, f.Line, f.Func, f.Value, maxCyclomaticComplexity)
		case f.Value > ceiling:
			t.Errorf("%s:%d (%s): cyclomatic complexity %d exceeds ratchet ceiling %d", f.File, f.Line, f.Func, f.Value, ceiling)
		case f.Value < ceiling:
			t.Errorf("%s:%d (%s): cyclomatic complexity %d is under its ceiling of %d: lower cyclomaticCeilings[%q] to %d", f.File, f.Line, f.Func, f.Value, ceiling, key, f.Value)
		}
	}
	for key, ceiling := range cyclomaticCeilings {
		if ceiling <= maxCyclomaticComplexity {
			t.Errorf("cyclomaticCeilings[%q] = %d is <= cap %d: remove stale entry", key, ceiling, maxCyclomaticComplexity)
			continue
		}
		if !seen[key] {
			t.Errorf("cyclomaticCeilings lists %q (ceiling %d), which is now <= %d or gone: remove its entry", key, ceiling, maxCyclomaticComplexity)
		}
	}
}

func annotateFileSeverity(findings []hygieneFinding, ceilings map[string]int) []hygieneFinding {
	counts := map[string]int{}
	for _, f := range findings {
		counts[f.File]++
	}
	for i := range findings {
		c := ceilings[findings[i].File]
		if c > 0 && counts[findings[i].File] <= c {
			findings[i].Severity = "ratcheted"
		} else {
			findings[i].Severity = "blocked"
		}
	}
	return findings
}

func assertFileRatchet(t *testing.T, dirs []string, findings []hygieneFinding, ceilings map[string]int, mapName, reason string) {
	t.Helper()
	counts := map[string]int{}
	for _, f := range findings {
		counts[f.File]++
	}
	seen := map[string]bool{}
	for _, dir := range dirs {
		for _, path := range nonTestGoFiles(t, dir) {
			seen[path] = true
			count, ceiling := counts[path], ceilings[path]
			switch {
			case count > ceiling:
				t.Errorf("%s: %d %s (ceiling %d)", path, count, reason, ceiling)
			case count < ceiling:
				t.Errorf("%s: %d findings, under its ceiling of %d: lower %s[%q] to %d", path, count, ceiling, mapName, path, count)
			}
		}
	}
	for file, ceiling := range ceilings {
		if !seen[file] {
			t.Errorf("%s lists %s, which is gone: remove its entry", mapName, file)
			continue
		}
		if ceiling <= 0 {
			t.Errorf("%s lists %s with non-positive ceiling %d: remove its entry", mapName, file, ceiling)
		}
	}
}
