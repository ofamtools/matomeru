package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/mcpui"
	"ofam.dev/ochazuke/ui"
)

func TestPagePortalRendersStoredContent(t *testing.T) {
	for _, tc := range []struct {
		name    string
		fields  map[string]any
		markers []string
	}{
		{"encoded nested tree", map[string]any{"root": `{
			"kind":"vstack",
			"children":[{"kind":"panel","border":true,"children":[
				{"kind":"table","props":{"columns":[{"key":"item","title":"Item"}],"rows":[{"item":"TABLE-CONTENT"}]}},
				{"kind":"markdown","props":{"text":"NESTED-CONTENT"}}
			]}]
		}`}, []string{"TABLE-CONTENT", "NESTED-CONTENT"}},
		{"body without root", map[string]any{"body": "BODY-CONTENT", "summary": "SUMMARY-IGNORED"}, []string{"BODY-CONTENT"}},
		{"summary without root", map[string]any{"summary": "SUMMARY-CONTENT"}, []string{"SUMMARY-CONTENT"}},
		{"markdown outside empty root", map[string]any{"root": map[string]any{"kind": "markdown"}, "markdown": "FIELD-MARKDOWN"}, []string{"FIELD-MARKDOWN"}},
		{"scroll with nested flex content", map[string]any{"root": json.RawMessage(`{
			"kind":"scroll","border":true,"box":{"fixedH":1},
			"children":[{"kind":"hstack","children":[{"kind":"panel","border":true,"children":[
				{"kind":"markdown","props":{"markdown":"FLEX-CONTENT"}}
			]}]}]
		}`)}, []string{"FLEX-CONTENT"}},
		{"unbounded inner scroll", map[string]any{"root": json.RawMessage(`{
			"kind":"vstack","children":[
				{"kind":"scroll","border":true,"children":[
					{"kind":"markdown","props":{"markdown":"INNER-SCROLL-CONTENT"}}
				]}
			]
		}`)}, []string{"INNER-SCROLL-CONTENT"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := setupNavTreeFixture(t)
			a, d := f.app, f.driver
			tc.fields["title"] = tc.name
			tc.fields["task"] = f.proj.Ref()
			page, _, err := a.Store.Create(data.CreateInput{Kind: data.KindPage, Fields: tc.fields}, User)
			if err != nil {
				t.Fatal(err)
			}
			d.Send(resize(120, 36))
			d.Keys("5", "enter", "j", "j", "j", "j", "l")
			target := projectNavNodePrefix + "pages." + page.ID
			for i := 0; i < 3 && a.Focus() != target; i++ {
				d.Keys("j")
			}
			if a.Focus() != target {
				t.Fatalf("expected page row %q, got %q", target, a.Focus())
			}
			requirePageMarkers(t, d, tc.markers...)
			d.Keys("enter")
			if a.activeSitePageID() != page.ID {
				t.Fatalf("active page = %q, want %q", a.activeSitePageID(), page.ID)
			}
			d.Send(resize(80, 24))
			requirePageMarkers(t, d, tc.markers...)
		})
	}
}

func TestPagePickerRendersMCPPage(t *testing.T) {
	for _, name := range []string{"attached", "unattached", "missing pane"} {
		t.Run(name, func(t *testing.T) {
			a, d := demo(t)
			paneID := ""
			switch name {
			case "attached":
				pane, ok := a.SelectedPane()
				if !ok {
					t.Fatal("expected selected pane")
				}
				paneID = pane.ID
			case "missing pane":
				paneID = "not-yet-synced"
			}
			raw := fmt.Sprintf(`{"title":"Picker reference","pane":%q,
				"root":{"kind":"scroll","border":true,"box":{"fixedH":1},"children":[
					{"kind":"markdown","props":{"markdown":"## PICKER-CONTENT"}}
				]}
			}`, paneID)
			out, err := a.Router.Call(t.Context(), User, mcpui.ToolCreatePage, json.RawMessage(raw))
			if err != nil {
				t.Fatal(err)
			}
			d.Send(resize(80, 24))
			d.Keys("2", "'")
			d.Type("Picker reference")
			d.Keys("enter")
			if a.Modal() != "" {
				t.Fatalf("picker remained open: %q", a.Modal())
			}
			want := out.(*mcpui.PageResult)
			if name == "attached" {
				if a.activeSitePageID() != want.ID {
					t.Fatalf("active site page = %q, want %q", a.activeSitePageID(), want.ID)
				}
			} else if shown, ok := a.Showing(); !ok || shown != want.Ref || findNode(a, "show.content.root") == nil {
				t.Fatalf("picker did not open rendered canvas for %s", want.ID)
			}
			requirePageMarkers(t, d, "PICKER-CONTENT")
		})
	}
}

func TestPageCanvasLinkRendersPageWithoutPane(t *testing.T) {
	a, d := demo(t)
	pane, ok := a.SelectedPane()
	if !ok {
		t.Fatal("expected selected pane")
	}
	target, err := a.Router.CreatePage(User, mcpui.CreatePageArgs{
		Title: "Unattached reference",
		Root:  ui.ViewNode{Kind: ui.KindMarkdown, Props: map[string]any{"markdown": "## LINKED-CONTENT"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	link := ui.InlineRun{Kind: "link", Label: "Open reference", Ref: &target.Ref}
	source, err := a.Router.CreatePage(User, mcpui.CreatePageArgs{
		Pane: pane.ID, Title: "Link source",
		Root: ui.ViewNode{ID: "page-source-link", Kind: ui.KindText, Props: map[string]any{"runs": []ui.InlineRun{link}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	a.Shell.State = a.jumpToPage(a.State(), source.ID)
	d.Keys("enter")
	if a.Focus() != "page-source-link" {
		t.Fatalf("source link not focused: %q", a.Focus())
	}
	d.Keys("enter")
	requirePageMarkers(t, d, "LINKED-CONTENT")
}

func TestPageCanvasStoredMarkdownRemainsInteractive(t *testing.T) {
	a, d := demo(t)
	pane, ok := a.SelectedPane()
	if !ok {
		t.Fatal("expected selected pane")
	}
	page, _, err := a.Store.Create(data.CreateInput{Kind: data.KindPage, Fields: map[string]any{
		"title": "Stored Markdown checklist",
		"pane":  pane.Ref(),
		"root":  `{"kind":"markdown","id":"legacy-checklist","props":{"text":"- [ ] CHECKLIST-CONTENT"}}`,
	}}, User)
	if err != nil {
		t.Fatal(err)
	}
	a.Shell.State = a.jumpToPage(a.State(), page.ID)
	d.Keys("enter")
	requirePageMarkers(t, d, "☐ CHECKLIST-CONTENT")
	d.Keys("space")
	requirePageMarkers(t, d, "☑ CHECKLIST-CONTENT")
	saved, err := a.Store.Get(page.Ref())
	if err != nil {
		t.Fatal(err)
	}
	root, err := mcpui.PageRoot(*saved)
	if err != nil {
		t.Fatal(err)
	}
	if got := root.Props["markdown"]; got != "- [x] CHECKLIST-CONTENT" {
		t.Fatalf("stored Markdown = %q", got)
	}
}

func TestPageShowRendersNestedContent(t *testing.T) {
	a, d := demo(t)
	page, _, err := a.Store.Create(data.CreateInput{Kind: data.KindPage, Fields: map[string]any{
		"title": "Show this page",
		"root": ui.ViewNode{ID: "named-root-panel", Kind: ui.KindPanel, Children: []ui.ViewNode{
			{Kind: ui.KindMarkdown, Props: map[string]any{"markdown": "SHOW-CONTENT"}},
		}},
	}}, User)
	if err != nil {
		t.Fatal(err)
	}
	a.Shell.State = a.show(a.State(), page.Ref())
	d.Send(resize(120, 36))
	requirePageMarkers(t, d, "SHOW-CONTENT")
}

func requirePageMarkers(t *testing.T, d *Driver, markers ...string) {
	t.Helper()
	frame := d.Frame()
	for _, marker := range markers {
		if !strings.Contains(frame, marker) {
			t.Errorf("missing %q in frame:\n%s", marker, frame)
		}
	}
}
