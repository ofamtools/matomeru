package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ofam.dev/matomeru/ext"
	"ofam.dev/ochazuke/core"
)

func TestExternalCatalogBrowseCacheAndShadow(t *testing.T) {
	a, d := demo(t)
	dir := t.TempDir()
	bin := filepath.Join(dir, "o-links")
	script := `#!/bin/sh
case "$1" in
  catalog)
    cat <<'EOF'
{
  "name": "o-links",
  "version": "1.0",
  "schemas": [{
    "kind": "links.bookmark",
    "title": "Bookmark",
    "plural": "Bookmarks",
    "summaryField": "title",
    "defaultColumns": ["title", "url"],
    "fields": [
      {"name": "title", "type": "string", "required": true},
      {"name": "url", "type": "string"}
    ]
  }]
}
EOF
    ;;
  query)
    cat <<'EOF'
{
  "total": 2,
  "items": [
    {"kind": "links.bookmark", "id": "b1", "fields": {"title": "Ochazuke Docs", "url": "https://ofam.dev/ochazuke"}},
    {"kind": "links.bookmark", "id": "b2", "fields": {"title": "Go Spec", "url": "https://go.dev/ref/spec"}}
  ]
}
EOF
    ;;
esac
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	m, err := ext.LoadCatalog(t.Context(), bin)
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	if err := a.MountCatalog(bin, m); err != nil {
		t.Fatalf("MountCatalog: %v", err)
	}

	// Browse the mounted external noun via g n -> triggers async ReadCache fetch.
	d.Keys("g", "n")
	d.Type("bookmarks")
	d.Keys("enter")
	if a.workKind() != "links.bookmark" {
		t.Fatalf("workKind = %q, want links.bookmark", a.workKind())
	}

	// Wait for the background ReadCache fetch to populate and apply on the UI loop.
	a.WaitBackground()
	a.Shell.Update(wakeMsg())
	frame := d.Frame()
	if !strings.Contains(frame, "Ochazuke Docs") || !strings.Contains(frame, "Go Spec") {
		t.Fatalf("external bookmarks not shown in Work table:\n%s", frame)
	}

	// Starring an external record creates its lazy shadow in SQLite without writing to the tool.
	d.Keys("s")
	ref := core.Ref{Kind: "links.bookmark", ID: "b1"}
	shadow, err := a.Store.Get(ref)
	if err != nil || !shadow.IsStarred() {
		t.Fatalf("shadow after star: rec=%+v err=%v", shadow, err)
	}
	if !shadow.ReadOnly["title"] || !shadow.ReadOnly["url"] {
		t.Fatalf("expected tool-owned fields to be ReadOnly on shadow: %+v", shadow.ReadOnly)
	}

	// Opening the external record's Show page works like any native record.
	d.Keys("enter")
	if shown, ok := a.Showing(); !ok || shown != ref {
		t.Fatalf("showing = %v (ok=%v), want %v", shown, ok, ref)
	}
	if sf := d.Frame(); !strings.Contains(sf, "Ochazuke Docs") || !strings.Contains(sf, "starred") {
		t.Fatalf("external Show page:\n%s", sf)
	}
}
