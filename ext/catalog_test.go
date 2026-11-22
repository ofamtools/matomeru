package ext

import (
	"context"
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
)

// linksTool answers the catalog protocol in the wording of
// examples/extension-api: nouns and verbs.
const linksTool = `case "$1" in
catalog) cat <<'EOF'
{"name": "o-links", "version": "0.1.0",
 "nouns": [{"kind": "links.bookmark", "title": "Bookmark", "plural": "Bookmarks", "summaryField": "title",
            "fields": [{"name": "title", "type": "string"}, {"name": "url", "type": "string"}, {"name": "tag", "type": "string"}],
            "defaultColumns": ["title", "url"], "uniqueFields": ["url"]}],
 "verbs": [{"id": "links.bookmark.query", "command": ["query"], "summary": "Find bookmarks", "params": [{"name": "text", "type": "string"}]},
           {"id": "links.bookmark.open", "command": ["open"], "params": [{"name": "id", "type": "string", "required": true}]}],
 "bindings": [{"scope": "links.bookmark", "mode": "normal", "keys": ["enter"], "command": "links.bookmark.open"}],
 "views": {"empty": {"id": "links.empty", "kind": "text", "props": {"text": "No bookmarks yet."}}}}
EOF
;;
query) cat <<'EOF'
{"items": [{"kind": "links.bookmark", "id": "ochazuke", "fields": {"title": "Ochazuke", "url": "https://ofam.dev/ochazuke", "tag": "ui"}},
           {"kind": "links.bookmark", "id": "mcp", "fields": {"title": "MCP", "url": "https://modelcontextprotocol.io", "tag": "agents"}},
           {"kind": "links.folder", "id": "inbox", "fields": {"title": "Inbox"}}],
 "total": 3}
EOF
;;
*) echo "o-links: unknown command $1" >&2; exit 2 ;;
esac
`

func TestLoadAndMountCatalog(t *testing.T) {
	store := openStore(t)
	bin := script(t, t.TempDir(), "o-links", linksTool, 0o755)
	m, err := LoadCatalog(context.Background(), bin)
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "o-links" || m.Version != "0.1.0" || len(m.Schemas) != 1 || len(m.Commands) != 2 || len(m.Bindings) != 1 {
		t.Fatalf("manifest = %+v", m)
	}
	if c := m.Commands[0]; c.ID != "links.bookmark.query" || c.Description != "Find bookmarks" || len(c.Params) != 1 {
		t.Fatalf("command = %+v", c)
	}
	if b := m.Bindings[0]; b.Scope != "links.bookmark" || b.Command != "links.bookmark.open" || b.Keys[0] != "enter" {
		t.Fatalf("binding = %+v", b)
	}
	if v := m.Views["empty"]; v.Kind != ui.KindText || v.ID != "links.empty" {
		t.Fatalf("view = %+v", v)
	}
	if err := MountCatalog(store.Registry(), m); err != nil {
		t.Fatal(err)
	}
	schema, ok := store.Registry().Get("links.bookmark")
	if !ok || schema.Plural != "Bookmarks" || len(schema.Fields) != 3 || schema.UniqueFields[0] != "url" {
		t.Fatalf("mounted schema = %+v (%v)", schema, ok)
	}

	// The same manifest in the struct's own wording reads the same.
	own, err := ParseManifest([]byte(`{"name": "o-links", "schemas": [{"kind": "links.bookmark"}], "commands": [{"id": "links.bookmark.query", "description": "Find"}]}`))
	if err != nil || own.Schemas[0].Kind != "links.bookmark" || own.Commands[0].Description != "Find" {
		t.Fatalf("own wording: %+v, %v", own, err)
	}
	for doc, want := range map[string]string{
		`{"schemas": [{"kind": "x"}]}`:                               "no name",
		`{"name": "t", "schemas": [{"title": "X"}]}`:                 "no kind",
		`{"name": "t", "nouns": [{"kind": "x"}, {"kind": "x"}]}`:     "declared twice",
		`{"name": "t", "commands": [{"description": "nameless"}]}`:   "no id",
		`{"name": "t", "bindings": [{"scope": "x", "keys": ["a"]}]}`: "no command",
		`not json`: "not a manifest",
	} {
		if _, err := ParseManifest([]byte(doc)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("ParseManifest(%s) = %v, want %q", doc, err, want)
		}
	}
	if _, err := LoadCatalog(context.Background(), script(t, t.TempDir(), "o-broken", "echo no catalog here >&2; exit 3\n", 0o755)); err == nil || !strings.Contains(err.Error(), "no catalog here") {
		t.Fatalf("a tool with no catalog: %v", err)
	}
}

// Star, archive, snooze and a changed field of an external record live in
// Matomeru's shadow of it and are laid over what the tool returns.
func TestQueryExternalMergesLocalOverrides(t *testing.T) {
	store := openStore(t)
	bin := script(t, t.TempDir(), "o-links", linksTool, 0o755)
	ctx := context.Background()
	owner := core.Actor{Kind: "user", ID: "owner"}

	res, err := QueryExternal(ctx, bin, "links.bookmark", store)
	if err != nil || res.Total != 2 || len(res.Items) != 2 {
		t.Fatalf("query: %+v, %v", res, err)
	}
	if first := res.Items[0]; first.StarStatus != data.StarUnstarred || first.ArchiveStatus != data.ArchiveActive || len(first.Local) != 0 {
		t.Fatalf("an untouched external record = %+v", first)
	}
	if all, _ := QueryExternal(ctx, bin, "", store); all.Total != 3 {
		t.Fatalf("every kind: %d items", all.Total)
	}
	if n, _ := store.Query(data.Query{Kind: "links.bookmark"}); n.Total != 0 {
		t.Fatal("a query wrote to the store")
	}

	shadow, err := Shadow(store, res.Items[0], owner)
	if err != nil || shadow.ID != "ochazuke" || !shadow.ReadOnly["title"] {
		t.Fatalf("shadow = %+v, %v", shadow, err)
	}
	if again, err := Shadow(store, res.Items[0], owner); err != nil || again.ChangeSetNumber != shadow.ChangeSetNumber {
		t.Fatalf("a second Shadow wrote again: %v", err)
	}
	if _, _, err := store.Star(shadow.Ref(), owner); err != nil {
		t.Fatal(err)
	}
	// The tool's title is read-only here: the change becomes a local override.
	if _, _, err := store.Patch(data.PatchInput{Ref: shadow.Ref(), Fields: map[string]any{"title": "Ochazuke (mine)"}}, owner); err != nil {
		t.Fatal(err)
	}
	other, err := Shadow(store, res.Items[1], owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Archive(other.Ref(), owner); err != nil {
		t.Fatal(err)
	}

	res, err = QueryExternal(ctx, bin, "links.bookmark", store)
	if err != nil {
		t.Fatal(err)
	}
	first, second := res.Items[0], res.Items[1]
	if !first.IsStarred() || first.Text("title") != "Ochazuke (mine)" || first.Fields["title"] != "Ochazuke" || first.Text("url") != "https://ofam.dev/ochazuke" {
		t.Fatalf("merged first = %+v", first)
	}
	if !second.IsArchived() || second.ArchivedAt == nil || second.IsStarred() {
		t.Fatalf("merged second = %+v", second)
	}
	if _, err := Shadow(store, data.Record{Kind: "links.bookmark"}, owner); err == nil {
		t.Fatal("a record with no id was shadowed")
	}
}
