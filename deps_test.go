package matomeru_deps_test

import (
	"bytes"
	"encoding/json"
	"io"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

const (
	mod      = "ofam.dev/matomeru"
	ochazuke = "ofam.dev/ochazuke"
	sqlite   = "modernc.org/sqlite"
)

type pkgError struct {
	Err string `json:"Err"`
}

type pkgInfo struct {
	ImportPath   string     `json:"ImportPath"`
	Standard     bool       `json:"Standard"`
	Imports      []string   `json:"Imports"`
	TestImports  []string   `json:"TestImports"`
	XTestImports []string   `json:"XTestImports"`
	Deps         []string   `json:"Deps"`
	Incomplete   bool       `json:"Incomplete"`
	Error        *pkgError  `json:"Error"`
	DepsErrors   []pkgError `json:"DepsErrors"`
}

func runGoList(t *testing.T, args ...string) []pkgInfo {
	t.Helper()
	cmd := exec.Command("go", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go %s failed: %v\nstderr: %s", strings.Join(args, " "), err, stderr.String())
	}
	var pkgs []pkgInfo
	dec := json.NewDecoder(&stdout)
	for {
		var p pkgInfo
		err := dec.Decode(&p)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("decode go %s output: %v", strings.Join(args, " "), err)
		}
		pkgs = append(pkgs, p)
	}
	return pkgs
}

// policy states what a package may import outside the standard library.
type policy struct {
	// allow lists the exact non-stdlib import paths the package and its
	// tests may import directly.
	allow []string
	// forbidDeps lists substrings no transitive dependency may contain.
	forbidDeps []string
}

// Bubble Tea, and the Ochazuke adapter that owns it, stay out of every
// foundation package, direct or transitive.
var noTea = []string{"bubbletea", ochazuke + "/tea"}

var policies = map[string]policy{
	// The module root holds only this test.
	mod: {},
	// The Data API: no UI of any kind, direct or transitive.
	mod + "/data": {
		allow:      []string{sqlite, ochazuke + "/core"},
		forbidDeps: append([]string{"lipgloss", ochazuke + "/ui", ochazuke + "/widgets", ochazuke + "/buffer"}, noTea...),
	},
	// MCP Open UI: ViewNode data over the store.
	mod + "/mcpui": {
		allow:      []string{mod + "/data", ochazuke + "/core", ochazuke + "/ui"},
		forbidDeps: noTea,
	},
	// ViewNode generators.
	mod + "/views": {
		allow: []string{
			mod + "/data", ochazuke + "/core", ochazuke + "/ui", ochazuke + "/layout",
			ochazuke + "/widgets/table", ochazuke + "/widgets/form", ochazuke + "/widgets/picker",
		},
		forbidDeps: noTea,
	},
}

func TestDependencyBoundaries(t *testing.T) {
	pkgs := runGoList(t, "list", "-json", "./...")

	// Standard-library membership, including everything tests pull in.
	std := map[string]bool{}
	for _, p := range runGoList(t, "list", "-deps", "-test", "-json", "./...") {
		if p.Standard {
			std[p.ImportPath] = true
		}
	}

	byPath := map[string]pkgInfo{}
	for _, p := range pkgs {
		if p.Error != nil {
			t.Fatalf("package %s error: %s", p.ImportPath, p.Error.Err)
		}
		if p.Incomplete || len(p.DepsErrors) > 0 {
			t.Fatalf("package %s has incomplete metadata or deps errors: %+v", p.ImportPath, p.DepsErrors)
		}
		byPath[p.ImportPath] = p
	}
	for _, req := range []string{"data", "mcpui", "views"} {
		if _, ok := byPath[mod+"/"+req]; !ok {
			t.Fatalf("required package %q missing from go list ./...", mod+"/"+req)
		}
	}

	for _, p := range pkgs {
		pol, governed := policies[p.ImportPath]
		if !governed {
			t.Errorf("unexpected package %q: add a policy to deps_test.go", p.ImportPath)
			continue
		}
		for kind, list := range map[string][]string{"Imports": p.Imports, "TestImports": p.TestImports, "XTestImports": p.XTestImports} {
			for _, imp := range list {
				if imp == "C" {
					t.Errorf("%s %s imports the cgo pseudo-package", p.ImportPath, kind)
				}
				if imp == p.ImportPath || std[imp] { // xtests import their own package
					continue
				}
				if !slices.Contains(pol.allow, imp) {
					t.Errorf("%s %s %q is outside its allowlist %v", p.ImportPath, kind, imp, pol.allow)
				}
			}
		}
		for _, dep := range p.Deps {
			for _, f := range pol.forbidDeps {
				if strings.Contains(dep, f) {
					t.Errorf("%s has forbidden transitive dependency %q", p.ImportPath, dep)
				}
			}
		}
	}
}
