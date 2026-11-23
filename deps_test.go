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
	oden     = "ofam.dev/oden"
	sqlite   = "modernc.org/sqlite"
	// bubbletea is allowed only in app and cmd/mtm.
	bubbletea = "charm.land/bubbletea/v2"
	// examples/... are programs for readers: they may import anything.
	examples = mod + "/examples/"
	// The PTY and the VT emulator are allowed only in ptyhost.
	pty = "github.com/creack/pty"
	vt  = "tuios.dev/vt"
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

// noUI keeps a package free of every UI layer, not only Bubble Tea.
var noUI = append([]string{"lipgloss", ochazuke + "/ui", ochazuke + "/widgets", ochazuke + "/buffer"}, noTea...)

var policies = map[string]policy{
	// The module root holds only this test.
	mod: {},
	// The Data API: no UI of any kind, direct or transitive.
	mod + "/data": {
		allow:      []string{sqlite, ochazuke + "/core"},
		forbidDeps: noUI,
	},
	// MCP Open UI: ViewNode data over the store.
	mod + "/mcpui": {
		allow:      []string{mod + "/data", mod + "/triage", ochazuke + "/core", ochazuke + "/ui"},
		forbidDeps: noTea,
	},
	// ViewNode generators.
	mod + "/views": {
		allow: []string{
			mod + "/data", ochazuke + "/core", ochazuke + "/ui", ochazuke + "/layout", ochazuke + "/expr",
			ochazuke + "/widgets/table", ochazuke + "/widgets/form", ochazuke + "/widgets/picker",
		},
		forbidDeps: noTea,
	},
	// Runs: the one place commands are executed, as run records in the store.
	// No UI of any kind.
	mod + "/runs": {
		allow:      []string{mod + "/data", ochazuke + "/core"},
		forbidDeps: noUI,
	},
	// Extensions: discovery over the store, execution only through runs. A
	// catalog's views are ViewNode data, so ext reads ui's types; it draws
	// nothing and stays free of Bubble Tea.
	mod + "/ext": {
		allow:      []string{mod + "/data", mod + "/runs", mod + "/actions", ochazuke + "/core", ochazuke + "/ui"},
		forbidDeps: noTea,
	},
	// File handlers: rules as data and the matching of a target to them.
	// Standard library only.
	mod + "/handlers": {
		forbidDeps: append([]string{mod + "/data", sqlite, ochazuke}, noUI...),
	},
	// Triage: rules over signals, through the Data API and the expression
	// language. No UI of any kind.
	mod + "/triage": {
		allow:      []string{mod + "/data", ochazuke + "/core", ochazuke + "/expr"},
		forbidDeps: noUI,
	},
	// PTY sessions: a PTY, a VT emulator and the cursor type of Ochazuke's
	// buffer. It knows nothing of the store, the views or the shell.
	mod + "/ptyhost": {
		allow:      []string{pty, vt, ochazuke + "/buffer"},
		forbidDeps: append([]string{mod + "/data", sqlite, ochazuke + "/ui", ochazuke + "/widgets"}, noTea...),
	},
	// The v1 importer: SQLite to read v1, the Data API to write v2. No UI.
	mod + "/migrate": {
		allow:      []string{sqlite, mod + "/data", ochazuke + "/core"},
		forbidDeps: noUI,
	},
	// Local self-hosted daemon: Unix-socket PTY persistence, HTTP MCP, and schedule ticker.
	mod + "/daemon": {
		allow:      []string{mod + "/data", mod + "/mcpui", mod + "/ptyhost", ochazuke + "/core"},
		forbidDeps: noTea,
	},
	// Agent hook handler: pure stdlib wire client for the daemon Unix socket.
	mod + "/agenthook": {
		forbidDeps: append([]string{mod + "/data", sqlite, ochazuke}, noUI...),
	},
	// Standalone fast hook binary: stdlib + agenthook only.
	mod + "/cmd/mtm-hook": {
		allow:      []string{mod + "/agenthook"},
		forbidDeps: append([]string{mod + "/data", sqlite, ochazuke}, noUI...),
	},
	// Harness registry and harnesses.json config: pure stdlib.
	mod + "/harness": {
		forbidDeps: append([]string{mod + "/data", sqlite, ochazuke}, noUI...),
	},
	// High-signal context items extractor: pure stdlib.
	mod + "/contextitems": {
		forbidDeps: append([]string{mod + "/data", sqlite, ochazuke}, noUI...),
	},
	// User-defined actions.json loader and namespace action grouping: core only, no UI.
	mod + "/actions": {
		allow:      []string{ochazuke + "/core"},
		forbidDeps: noUI,
	},
	// The application: the only package that wires the foundation onto the
	// engine and its Bubble Tea shell.
	mod + "/app": {
		allow: []string{
			mod + "/data", mod + "/mcpui", mod + "/views", mod + "/runs", mod + "/ext", mod + "/ptyhost", mod + "/handlers", mod + "/triage",
			mod + "/daemon", mod + "/harness", mod + "/contextitems", mod + "/actions", bubbletea,
			ochazuke + "/core", ochazuke + "/ui", ochazuke + "/layout", ochazuke + "/expr", ochazuke + "/config", ochazuke + "/tea",
			ochazuke + "/widgets/table", ochazuke + "/widgets/form", ochazuke + "/widgets/picker", ochazuke + "/widgets/pathpicker",
			ochazuke + "/widgets/bindings", ochazuke + "/widgets/markdown", ochazuke + "/widgets/input", ochazuke + "/widgets/commandline",
			ochazuke + "/widgets/palette", ochazuke + "/widgets/toast", ochazuke + "/widgets/hints",
		},
	},
	// The binary: oden + ochazuke/cli around app, data, mcpui, daemon, agenthook, harness, ptyhost and migrate.
	mod + "/cmd/mtm": {
		allow: []string{mod + "/app", mod + "/data", mod + "/mcpui", mod + "/migrate", mod + "/daemon", mod + "/agenthook", mod + "/harness", mod + "/ptyhost", ochazuke + "/core", ochazuke + "/config", ochazuke + "/cli", oden, bubbletea},
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
	for _, req := range []string{"data", "mcpui", "views", "runs", "ext", "ptyhost", "migrate", "handlers", "triage", "app", "cmd/mtm",
		"examples/mcp-agent", "examples/extension-api", "examples/custom-schema"} {
		if _, ok := byPath[mod+"/"+req]; !ok {
			t.Fatalf("required package %q missing from go list ./...", mod+"/"+req)
		}
	}

	for _, p := range pkgs {
		pol, governed := policies[p.ImportPath]
		if strings.HasPrefix(p.ImportPath, examples) {
			continue // examples show the whole surface: they import what they like
		}
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
