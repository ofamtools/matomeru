// Command extension-api is a whole third-party extension in one file: a small
// bookmark tool in the shape of o-links. It imports nothing of Matomeru's but
// the ViewNode type, and talks to it only through its own command line.
//
// Level 1 is the probe Matomeru's Extensions space already reads:
//
//	o-links --version                    one line; running at all means healthy
//	o-links capabilities --json          {"summary","version","capabilities","commands"}
//
// Level 2 is the JSON protocol this example shows: every command prints one
// JSON document, in the shapes Matomeru's own tools use, so an agent or a
// script can pass them straight on.
//
//	o-links catalog                      the nouns (schemas) and verbs the tool has
//	o-links query [TEXT]                 {"items": [record...], "total": n}
//	o-links open ID                      a ui.ViewNode card tree: the root of a ui.create_page call
//
//	go run ./examples/extension-api --smoke   runs every command, discovers the binary as an extension, exits 0
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/ext"
	"ofam.dev/matomeru/mcpui"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
)

const (
	name    = "o-links"
	version = "0.1.0"
)

// bookmark is the tool's own data. A real tool reads a file or a service.
type bookmark struct{ ID, Title, URL, Tag, Note string }

var bookmarks = []bookmark{
	{"ochazuke", "Ochazuke", "https://ofam.dev/ochazuke", "ui", "The modal UI kernel: one keymap, ViewNodes, widgets."},
	{"matomeru", "Matomeru", "https://ofam.dev/matomeru", "ui", "Projects, tasks and notes beside your panes."},
	{"mcp", "Model Context Protocol", "https://modelcontextprotocol.io", "agents", "How an agent calls ui.create_page."},
}

// schema is the noun the tool adds, in the shape of a Matomeru schema.
func schema() map[string]any {
	field := func(name, kind, desc string) map[string]any {
		return map[string]any{"name": name, "type": kind, "description": desc}
	}
	return map[string]any{"kind": "links.bookmark", "title": "Bookmark", "plural": "Bookmarks", "summaryField": "title",
		"fields":         []any{field("title", "string", "what it is"), field("url", "string", "where it is"), field("tag", "string", "one word"), field("note", "string", "why it was kept")},
		"defaultColumns": []string{"title", "url", "tag"}, "uniqueFields": []string{"url"}}
}

// record is a bookmark in the shape of a Matomeru record.
func record(b bookmark) map[string]any {
	return map[string]any{"kind": "links.bookmark", "id": b.ID, "fields": map[string]any{"title": b.Title, "url": b.URL, "tag": b.Tag, "note": b.Note}}
}

// card is what "open" prints: a ViewNode tree any page can hold.
func card(b bookmark) ui.ViewNode {
	return ui.ViewNode{ID: "bookmark." + b.ID, Kind: ui.KindVStack, Gap: 1, Children: []ui.ViewNode{
		{ID: "bookmark." + b.ID + ".card", Kind: ui.KindCard, Scope: "links.bookmark", Ref: &core.Ref{Kind: "links.bookmark", ID: b.ID},
			Focusable: true, Border: true, Title: b.Title, Badges: []ui.Badge{{Text: b.Tag, Tone: "info"}},
			Props: map[string]any{"summary": b.Note}},
		{ID: "bookmark." + b.ID + ".link", Kind: ui.KindMarkdown, Props: map[string]any{"markdown": "[" + b.URL + "](" + b.URL + ")"}},
	}}
}

// serve answers one command line. It is the whole extension.
func serve(args []string, out io.Writer) error {
	print := func(v any) error {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}
	cmd := strings.Join(args, " ")
	switch {
	case cmd == "--version":
		_, err := fmt.Fprintln(out, version)
		return err
	case cmd == "capabilities --json":
		return print(map[string]any{"summary": "Bookmarks: keep, find and open links.", "version": version,
			"capabilities": []string{"nouns: links.bookmark", "MCP: links.bookmark.query, links.bookmark.open"},
			"commands":     []string{"catalog", "query", "open"}})
	case cmd == "catalog":
		return print(map[string]any{"name": name, "version": version, "nouns": []any{schema()},
			"verbs": []any{
				map[string]any{"id": "links.bookmark.query", "command": []string{"query"}, "params": []any{map[string]any{"name": "text", "type": "string"}}, "returns": "records"},
				map[string]any{"id": "links.bookmark.open", "command": []string{"open"}, "params": []any{map[string]any{"name": "id", "type": "string", "required": true}}, "returns": "viewnode"},
			}})
	case len(args) >= 1 && args[0] == "query":
		needle := strings.ToLower(strings.Join(args[1:], " "))
		items := []any{}
		for _, b := range bookmarks {
			if strings.Contains(strings.ToLower(b.Title+" "+b.URL+" "+b.Tag+" "+b.Note), needle) {
				items = append(items, record(b))
			}
		}
		return print(map[string]any{"items": items, "total": len(items)})
	case len(args) == 2 && args[0] == "open":
		i := slices.IndexFunc(bookmarks, func(b bookmark) bool { return b.ID == args[1] })
		if i < 0 {
			return fmt.Errorf("no bookmark %q", args[1])
		}
		return print(card(bookmarks[i]))
	}
	return fmt.Errorf("usage: %s --version | capabilities --json | catalog | query [TEXT] | open ID", name)
}

// smoke is the host's side: it runs each command as a host would, checks the
// documents, has Matomeru discover this binary as an extension, and puts an
// opened card on a pane's page.
func smoke() error {
	run := func(into any, args ...string) error {
		var buf strings.Builder
		if err := serve(args, &buf); err != nil {
			return err
		}
		return json.Unmarshal([]byte(buf.String()), into)
	}
	var catalog struct {
		Nouns []data.Schema    `json:"nouns"`
		Verbs []map[string]any `json:"verbs"`
	}
	if err := run(&catalog, "catalog"); err != nil || len(catalog.Nouns) != 1 || catalog.Nouns[0].Kind != "links.bookmark" || len(catalog.Verbs) != 2 {
		return fmt.Errorf("catalog: %v %+v", err, catalog)
	}
	var found data.QueryResult
	if err := run(&found, "query", "kernel"); err != nil || found.Total != 1 || found.Items[0].Text("title") != "Ochazuke" {
		return fmt.Errorf("query: %v %+v", err, found)
	}
	var root ui.ViewNode
	if err := run(&root, "open", found.Items[0].ID); err != nil {
		return fmt.Errorf("open: %w", err)
	}
	if err := mcpui.ValidateNode(root); err != nil {
		return fmt.Errorf("open printed a tree Matomeru would refuse: %w", err)
	}
	if serve([]string{"open", "nope"}, io.Discard) == nil {
		return errors.New("open of a missing bookmark did not fail")
	}

	// The host: a store that takes the catalog's schema and the opened card.
	store, err := data.Open(":memory:")
	if err != nil {
		return err
	}
	defer store.Close()
	store.Registry().Register(catalog.Nouns...)
	router := mcpui.New(store)
	page, err := router.CreatePage(core.Actor{Kind: "agent", ID: name}, mcpui.CreatePageArgs{Pane: "p1", Title: "Bookmark: " + root.Children[0].Title, Root: root})
	if err != nil {
		return fmt.Errorf("the card on a page: %w", err)
	}

	// Level 1: Matomeru finds this very binary under the name o-links.
	self, err := os.Executable()
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "extension-api-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	if err := os.Symlink(self, filepath.Join(dir, name)); err != nil {
		return err
	}
	res, err := ext.New(ext.Options{Tools: []string{name}, ProbeTimeout: 20 * time.Second}).Discover(context.Background(), []string{dir}, store)
	if err != nil {
		return err
	}
	if r := res[0]; r.Status != ext.StatusHealthy || r.Version != version || !slices.Equal(r.Commands, []string{"catalog", "query", "open"}) {
		return fmt.Errorf("discovery: status %s, version %q, commands %v (%v)", r.Status, r.Version, r.Commands, r.Err)
	}
	fmt.Printf("extension-api: ok (catalog, query, open; discovered as %s %s; page %s holds the card)\n", name, version, page.ID)
	return nil
}

func main() {
	args := os.Args[1:]
	var err error
	if len(args) == 1 && args[0] == "--smoke" {
		err = smoke()
	} else {
		err = serve(args, os.Stdout)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, name+":", err)
		os.Exit(1)
	}
}
