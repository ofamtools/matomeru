package app

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"ofam.dev/matomeru/views"
)

type scannedSymbol struct {
	File      string
	Symbol    string
	StartLine int
	EndLine   int
	Kind      string // "func", "type", "const"
}

const maxScannedPerClass = 16

// mergedDomainOccurrences combines the curated static catalog with a fast,
// cached AST scan of the local Matomeru checkout when present on disk. Static
// symbols get their exact StartLine..EndLine refreshed from the real AST, and
// additional recurring declarations are appended per classification.
func (a *App) mergedDomainOccurrences(repoPath string) []views.DomainOccurrence {
	static := staticDomainOccurrences()
	repo := strings.TrimSpace(repoPath)
	if repo == "" {
		return static
	}
	fc := a.featuresCtrl()
	if fc.domainScanCached && fc.domainScanRepo == repo {
		return slices.Clone(fc.domainScanItems)
	}
	symbols := scanRepoGoSymbols(repo)
	if len(symbols) == 0 {
		fc.domainScanCached = true
		fc.domainScanRepo = repo
		fc.domainScanItems = static
		return slices.Clone(static)
	}
	merged := mergeStaticAndScannedDomain(static, symbols)
	fc.domainScanCached = true
	fc.domainScanRepo = repo
	fc.domainScanItems = merged
	return slices.Clone(merged)
}

func scanRepoGoSymbols(repo string) []scannedSymbol {
	var out []scannedSymbol
	fset := token.NewFileSet()
	for _, dir := range []string{"views", "app", "data"} {
		absDir := filepath.Join(repo, dir)
		info, err := os.Stat(absDir)
		if err != nil || !info.IsDir() {
			continue
		}
		out = append(out, scanGoDirSymbols(fset, repo, absDir)...)
	}
	return out
}

func scanGoDirSymbols(fset *token.FileSet, repo, absDir string) []scannedSymbol {
	var out []scannedSymbol
	_ = filepath.WalkDir(absDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		rel, rerr := filepath.Rel(repo, path)
		if rerr != nil {
			return nil
		}
		out = append(out, parseFileSymbols(fset, path, filepath.ToSlash(rel))...)
		return nil
	})
	return out
}

func parseFileSymbols(fset *token.FileSet, absPath, relPath string) []scannedSymbol {
	f, err := parser.ParseFile(fset, absPath, nil, 0)
	if err != nil {
		return nil
	}
	var out []scannedSymbol
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Name == nil {
				continue
			}
			out = append(out, scannedSymbol{
				File:      relPath,
				Symbol:    d.Name.Name,
				StartLine: fset.Position(d.Pos()).Line,
				EndLine:   fset.Position(d.End()).Line,
				Kind:      "func",
			})
		case *ast.GenDecl:
			out = append(out, parseGenDeclSymbols(fset, relPath, d)...)
		}
	}
	return out
}

func parseGenDeclSymbols(fset *token.FileSet, relPath string, d *ast.GenDecl) []scannedSymbol {
	var out []scannedSymbol
	for _, sp := range d.Specs {
		switch s := sp.(type) {
		case *ast.TypeSpec:
			if s.Name != nil {
				out = append(out, scannedSymbol{
					File:      relPath,
					Symbol:    s.Name.Name,
					StartLine: fset.Position(s.Pos()).Line,
					EndLine:   fset.Position(s.End()).Line,
					Kind:      "type",
				})
			}
		case *ast.ValueSpec:
			if d.Tok != token.CONST {
				continue
			}
			for _, id := range s.Names {
				if strings.HasPrefix(id.Name, "Mode") {
					out = append(out, scannedSymbol{
						File:      relPath,
						Symbol:    id.Name,
						StartLine: fset.Position(s.Pos()).Line,
						EndLine:   fset.Position(s.End()).Line,
						Kind:      "const",
					})
				}
			}
		}
	}
	return out
}

func mergeStaticAndScannedDomain(static []views.DomainOccurrence, symbols []scannedSymbol) []views.DomainOccurrence {
	byKey := make(map[string]scannedSymbol, len(symbols))
	for _, sym := range symbols {
		byKey[sym.File+"::"+sym.Symbol] = sym
	}
	seen := make(map[string]bool, len(static))
	out := make([]views.DomainOccurrence, 0, len(static)+32)
	for _, item := range static {
		key := item.Location.File + "::" + item.Location.Symbol
		seen[key] = true
		if sym, ok := byKey[key]; ok {
			item.Location.StartLine = sym.StartLine
			item.Location.EndLine = sym.EndLine
		}
		out = append(out, item)
	}
	addedPerClass := map[string]int{}
	for _, sym := range symbols {
		key := sym.File + "::" + sym.Symbol
		if seen[key] {
			continue
		}
		occ, ok := classifyScannedSymbol(sym)
		if !ok || addedPerClass[occ.Classification] >= maxScannedPerClass {
			continue
		}
		seen[key] = true
		addedPerClass[occ.Classification]++
		out = append(out, occ)
	}
	return out
}

func classifyScannedSymbol(sym scannedSymbol) (views.DomainOccurrence, bool) {
	class, glyph, tone, summary := matchSymbolClassification(sym)
	if class == "" {
		return views.DomainOccurrence{}, false
	}
	id := "scan." + class + "." + strings.ToLower(sym.Symbol)
	pages := inferPagesForFile(sym.File)
	return views.DomainOccurrence{
		ID:             id,
		Classification: class,
		Name:           sym.Symbol,
		Glyph:          glyph,
		Tone:           tone,
		Summary:        summary,
		Location: views.CodeLocation{
			File:      sym.File,
			StartLine: sym.StartLine,
			EndLine:   sym.EndLine,
			Symbol:    sym.Symbol,
		},
		RelatedPages: pages,
		Tags:         []string{"scanned", sym.Kind},
	}, true
}

type symbolClassRule struct {
	kind    string
	match   func(scannedSymbol) bool
	class   string
	glyph   string
	tone    string
	summary string
}

var symbolClassRules = []symbolClassRule{
	{
		kind:    "const",
		match:   func(s scannedSymbol) bool { return strings.HasPrefix(s.Symbol, "Mode") && len(s.Symbol) > 4 },
		class:   DomainModesKey,
		glyph:   "⛨",
		tone:    "info",
		summary: "Declared interaction mode constant.",
	},
	{
		kind:    "type",
		match:   func(s scannedSymbol) bool { return strings.HasSuffix(s.Symbol, "Modal") },
		class:   DomainComponentsKey,
		glyph:   "◫",
		tone:    "info",
		summary: "Modal overlay component implementing modal.Modal.",
	},
	{
		kind: "func",
		match: func(s scannedSymbol) bool {
			return strings.HasPrefix(s.File, "views/") && strings.HasSuffix(s.Symbol, "View") && isExportedName(s.Symbol)
		},
		class:   DomainViewsKey,
		glyph:   "▤",
		tone:    "accent",
		summary: "Declarative pure view builder.",
	},
	{
		kind: "func",
		match: func(s scannedSymbol) bool {
			return isExportedName(s.Symbol) && (strings.Contains(s.Symbol, "Indicator") || strings.Contains(s.Symbol, "Unread"))
		},
		class:   DomainIndicatorsKey,
		glyph:   "●",
		tone:    "accent",
		summary: "Visual indicator helper discovered in source.",
	},
	{
		kind:    "func",
		match:   func(s scannedSymbol) bool { return isExportedName(s.Symbol) && strings.Contains(s.Symbol, "Badge") },
		class:   DomainBadgesKey,
		glyph:   "◈",
		tone:    "info",
		summary: "Declarative badge builder discovered in source.",
	},
	{
		kind: "func",
		match: func(s scannedSymbol) bool {
			return strings.HasSuffix(s.Symbol, "Bindings") || strings.HasSuffix(s.Symbol, "CommandSpecs")
		},
		class:   DomainVerbsKey,
		glyph:   "⌘",
		tone:    "muted",
		summary: "Declarative command spec or binding provider.",
	},
	{
		kind: "func",
		match: func(s scannedSymbol) bool {
			return isExportedName(s.Symbol) && (strings.Contains(s.Symbol, "Attention") || strings.Contains(s.Symbol, "Signal"))
		},
		class:   DomainSignalsKey,
		glyph:   "⚡",
		tone:    "warning",
		summary: "Signal or attention routing helper discovered in source.",
	},
}

func matchSymbolClassification(sym scannedSymbol) (class, glyph, tone, summary string) {
	for _, r := range symbolClassRules {
		if sym.Kind == r.kind && r.match(sym) {
			return r.class, r.glyph, r.tone, r.summary
		}
	}
	return "", "", "", ""
}

func isExportedName(name string) bool {
	return len(name) > 0 && name[0] >= 'A' && name[0] <= 'Z'
}

var filePageHints = []struct {
	needle string
	page   string
}{
	{"panes", SpacePanes},
	{"site.go", SpacePanes},
	{"project_home", "project_pages"},
	{"project_workspace", "project_pages"},
	{"project_nav", "project_pages"},
	{"projects_space", SpaceProjects},
	{"queue", "queue"},
	{"threads", "threads"},
	{"sessions", "sessions"},
	{"focus_page", "focus"},
	{"outline", "outline"},
	{"show", "show"},
	{"features", SpaceFeatures},
	{"settings", "settings"},
	{"bindings", "bindings"},
}

func inferPagesForFile(relPath string) []string {
	low := strings.ToLower(relPath)
	for _, h := range filePageHints {
		if strings.Contains(low, h.needle) {
			return []string{h.page}
		}
	}
	return nil
}
