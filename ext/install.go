package ext

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/runs"
)

// Catalog group names.
const (
	CategoryCore     = "core"
	CategoryOptional = "optional"

	DefaultGoBinary  = "go"
	DefaultGoPrivate = "ofam.dev"
)

// CatalogEntry describes one installable OFAM extension tool.
type CatalogEntry struct {
	Name     string `json:"name"`
	Package  string `json:"package"`
	Summary  string `json:"summary"`
	Category string `json:"category"`
}

// DefaultCatalog lists the 20 published ofam.dev tools (14 core, 6 optional).
var DefaultCatalog = []CatalogEntry{
	// Core (14)
	{Name: "o-ai", Package: "ofam.dev/o-ai@latest", Summary: "AI provider catalog, model queries, and prompt helpers", Category: CategoryCore},
	{Name: "o-cmds", Package: "ofam.dev/o-cmds@latest", Summary: "Saved shell commands and runnable command catalogs", Category: CategoryCore},
	{Name: "o-csv", Package: "ofam.dev/o-csv@latest", Summary: "CSV inspection, querying, and tabular transforms", Category: CategoryCore},
	{Name: "o-df", Package: "ofam.dev/o-df@latest", Summary: "Disk usage and filesystem inspection", Category: CategoryCore},
	{Name: "o-fleet", Package: "ofam.dev/o-fleet@latest", Summary: "Multi-repo and fleet workspace orchestration", Category: CategoryCore},
	{Name: "o-gh", Package: "ofam.dev/o-gh@latest", Summary: "GitHub issues, pull requests, and CI checks", Category: CategoryCore},
	{Name: "o-git", Package: "ofam.dev/o-git@latest", Summary: "Git repositories, branches, diffs, and worktrees", Category: CategoryCore},
	{Name: "o-json", Package: "ofam.dev/o-json@latest", Summary: "JSON querying, formatting, and schema inspection", Category: CategoryCore},
	{Name: "o-lane", Package: "ofam.dev/o-lane@latest", Summary: "Agent work lanes, plans, and progress tracking", Category: CategoryCore},
	{Name: "o-md", Package: "ofam.dev/o-md@latest", Summary: "Markdown documents, outlines, and note browsing", Category: CategoryCore},
	{Name: "o-mermaid", Package: "ofam.dev/o-mermaid@latest", Summary: "Mermaid diagram rendering and preview", Category: CategoryCore},
	{Name: "o-proj", Package: "ofam.dev/o-proj@latest", Summary: "Project directory registry and workspace discovery", Category: CategoryCore},
	{Name: "o-questions", Package: "ofam.dev/o-questions@latest", Summary: "Agent questions, decisions, and Q&A records", Category: CategoryCore},
	{Name: "o-services", Package: "ofam.dev/o-services@latest", Summary: "Local background services and process supervision", Category: CategoryCore},

	// Optional (6)
	{Name: "o-family", Package: "ofam.dev/o-family@latest", Summary: "OFAM suite manifest, release checks, and family status", Category: CategoryOptional},
	{Name: "o-inventory", Package: "ofam.dev/o-inventory@latest", Summary: "Machine, tool, and workspace asset inventory", Category: CategoryOptional},
	{Name: "o-koe", Package: "ofam.dev/o-koe/cmd/o-koe@latest", Summary: "Voice dictation and audio transcription helper", Category: CategoryOptional},
	{Name: "o-mcp", Package: "ofam.dev/o-mcp@latest", Summary: "Model Context Protocol server inspection and calls", Category: CategoryOptional},
	{Name: "o-qbt", Package: "ofam.dev/o-qbt@latest", Summary: "qBittorrent transfer monitoring and control", Category: CategoryOptional},
	{Name: "o-slack", Package: "ofam.dev/o-slack@latest", Summary: "Slack channels, messages, and signal ingestion", Category: CategoryOptional},
}

// CatalogEntries returns a copy of DefaultCatalog.
func CatalogEntries() []CatalogEntry {
	return slices.Clone(DefaultCatalog)
}

// LookupCatalog finds an entry by extension name in DefaultCatalog.
func LookupCatalog(name string) (CatalogEntry, bool) {
	return lookupCatalogIn(DefaultCatalog, name)
}

func lookupCatalogIn(entries []CatalogEntry, name string) (CatalogEntry, bool) {
	for _, e := range entries {
		if e.Name == name {
			return e, true
		}
	}
	return CatalogEntry{}, false
}

// Catalog returns the manager's configured catalog, defaulting to DefaultCatalog.
func (m *Manager) Catalog() []CatalogEntry {
	if len(m.opts.Catalog) > 0 {
		return slices.Clone(m.opts.Catalog)
	}
	return CatalogEntries()
}

// GoPrivate returns the manager's default GOPRIVATE value.
func (m *Manager) GoPrivate() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.opts.GoPrivate
}

// SetIncludeCatalog toggles whether Discover includes uninstalled catalog tools.
func (m *Manager) SetIncludeCatalog(on bool) {
	m.mu.Lock()
	m.opts.IncludeCatalog = on
	m.mu.Unlock()
}

func (m *Manager) catalogMap() map[string]CatalogEntry {
	entries := m.Catalog()
	out := make(map[string]CatalogEntry, len(entries))
	for _, e := range entries {
		out[e.Name] = e
	}
	return out
}

func (m *Manager) lookupCatalog(name string) (CatalogEntry, bool) {
	if len(m.opts.Catalog) > 0 {
		if e, ok := lookupCatalogIn(m.opts.Catalog, name); ok {
			return e, true
		}
	}
	return LookupCatalog(name)
}

// validateInstallPkg rejects empty, whitespace-containing, flag-like, or
// malformed Go module install paths.
func validateInstallPkg(pkg string) error {
	if pkg == "" {
		return errors.New("ext: install package is empty")
	}
	if strings.HasPrefix(pkg, "-") {
		return fmt.Errorf("ext: invalid install package %q", pkg)
	}
	if !strings.Contains(pkg, "/") {
		return fmt.Errorf("ext: invalid install package %q: expected module path", pkg)
	}
	for _, r := range pkg {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return fmt.Errorf("ext: invalid install package %q: contains whitespace or control character", pkg)
		}
	}
	if strings.ContainsAny(pkg, ";|&$`<>\"'\\") {
		return fmt.Errorf("ext: invalid install package %q: contains disallowed characters", pkg)
	}
	return nil
}

// Install resolves extName's Go package and runs `go install <pkg>` through
// runner so its output is captured as a run record. It respects the manager's
// process cap and configured GoPrivate setting.
func (m *Manager) Install(ctx context.Context, extName string, runner *runs.Runner) (*data.Record, error) {
	return m.InstallWithGoPrivate(ctx, extName, m.GoPrivate(), runner)
}

// InstallWithGoPrivate is Install with an explicit GOPRIVATE override.
func (m *Manager) InstallWithGoPrivate(ctx context.Context, extName, goPrivate string, runner *runs.Runner) (*data.Record, error) {
	if runner == nil || runner.Store() == nil {
		return nil, errors.New("ext: runner is nil")
	}
	extName = strings.TrimSpace(extName)
	if extName == "" {
		return nil, errors.New("ext: extension name is empty")
	}

	pkg := ""
	if rec, err := runner.Store().GetByField(data.KindExtension, "name", extName); err == nil && rec != nil {
		pkg = strings.TrimSpace(rec.Text("installPkg"))
	} else if err != nil && !errors.Is(err, data.ErrNotFound) {
		return nil, err
	}
	if pkg == "" {
		if entry, ok := m.lookupCatalog(extName); ok {
			pkg = strings.TrimSpace(entry.Package)
		}
	}
	if pkg == "" {
		return nil, fmt.Errorf("ext: no install package known for %q", extName)
	}
	if err := validateInstallPkg(pkg); err != nil {
		return nil, err
	}

	if err := m.acquire(ctx); err != nil {
		return nil, err
	}
	goBin := m.opts.GoBinary
	if goBin == "" {
		goBin = DefaultGoBinary
	}
	var env []string
	if gp := strings.TrimSpace(goPrivate); gp != "" {
		env = []string{"GOPRIVATE=" + gp}
	}
	rec, err := runner.Start(ctx, runs.StartSpec{
		Title: "install " + extName,
		Args:  []string{goBin, "install", pkg},
		Env:   env,
		Actor: m.opts.Actor,
	})
	if rec == nil {
		m.release()
		return nil, err
	}
	done := runner.Done(rec.ID)
	go func() {
		<-done
		m.release()
	}()
	return rec, err
}
