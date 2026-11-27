package app

import (
	"encoding/json"
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/mcpui"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
)

func TestInteractiveCanvasTableLinksAndPersistentCheckboxes(t *testing.T) {
	a, d := demoLegacy(t)
	pane, ok := a.SelectedPane()
	if !ok {
		t.Fatal("expected selected pane")
	}

	mdChecklistAndLinks := strings.Join([]string{
		"## Review Checklist",
		"",
		"- [ ] Verify ALDI collections",
		"- [ ] Check blocked pages",
		"",
		"## Links",
		"",
		"- amazing-savings: [source](https://aldi.example/src) · [live](https://shop.example/live)",
		"- autumn: [draft](https://ipp.example/draft)",
	}, "\n")

	res, err := a.Router.CreatePage(User, mcpui.CreatePageArgs{
		Pane:  pane.ID,
		Slug:  "aldi-collections",
		Title: "ALDI migration — collections",
		Root: ui.ViewNode{
			Kind: ui.KindVStack,
			Gap:  1,
			Children: []ui.ViewNode{
				{
					Kind: ui.KindTable,
					Props: map[string]any{
						"columns": []any{
							map[string]any{"key": "status", "title": "Status"},
							map[string]any{"key": "page", "title": "Page"},
							map[string]any{"key": "target", "title": "Instacart target"},
						},
						"rows": []any{
							map[string]any{"status": "blocked", "page": "amazing-savings", "target": "rc-amazing-savings-specialbuy-top-picks"},
							map[string]any{"status": "live", "page": "autumn", "target": "rc-autumn-range"},
						},
					},
				},
				{
					Kind:  ui.KindMarkdown,
					Props: map[string]any{"markdown": mdChecklistAndLinks},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("CreatePage: %v", err)
	}

	// Open the canvas: focus lands immediately on the interactive table at row 0.
	a.Shell.State = a.jumpToPage(a.State(), res.ID)
	d.Keys("enter")
	if a.Mode() != ModePage {
		t.Fatalf("expected ModePage, got %q", a.Mode())
	}
	tableID := a.Focus()
	if tableID == "" || tableID == views.PageScrollID {
		t.Fatalf("expected focus on auto-enriched canvas table, got %q", tableID)
	}
	wsTable := a.canvasWidgetStateFor(res.ID, tableID)
	if wsTable.row != 0 {
		t.Fatalf("initial table row = %d, want 0", wsTable.row)
	}

	// j moves to row 1 in the table; enter opens the unclipped row detail reader; q closes it.
	d.Keys("j")
	if wsTable.row != 1 {
		t.Fatalf("after j, table row = %d, want 1", wsTable.row)
	}
	d.Keys("enter")
	if a.Modal() != "reader" {
		t.Fatalf("enter on table row did not open row detail reader: modal=%q", a.Modal())
	}
	if !strings.Contains(d.Frame(), "rc-autumn-range") {
		t.Fatalf("row detail reader missing unclipped value:\n%s", d.Frame())
	}
	d.Keys("q")
	if a.Modal() != "" || a.Mode() != ModePage {
		t.Fatalf("closing row detail did not return to ModePage: modal=%q mode=%q", a.Modal(), a.Mode())
	}

	// Pressing j past the last table row moves seamlessly into the Markdown block below.
	d.Keys("j")
	mdID := a.Focus()
	if mdID == tableID || mdID == views.PageScrollID {
		t.Fatalf("j past table bottom did not move to Markdown block: focus=%q", mdID)
	}

	// Markdown starts on checkbox 0; space toggles it and persists to SQLite.
	d.Keys("space")
	if !strings.Contains(d.Frame(), "☑ Verify ALDI collections") {
		t.Fatalf("toggled checkbox did not render ☑:\n%s", d.Frame())
	}
	stored, err := a.Store.Get(core.Ref{Kind: data.KindPage, ID: res.ID})
	if err != nil {
		t.Fatalf("Store.Get: %v", err)
	}
	rawRoot, _ := json.Marshal(stored.Fields["root"])
	if !strings.Contains(string(rawRoot), "- [x] Verify ALDI collections") {
		t.Fatalf("checkbox toggle did not persist to ui.page record:\n%s", string(rawRoot))
	}

	// j moves to checkbox 1, then j again moves to the first link line; l moves to the second link.
	d.Keys("j", "j")
	if !strings.Contains(a.Status(), "https://aldi.example/src") {
		t.Fatalf("expected status to show first link URL, got %q", a.Status())
	}
	d.Keys("l")
	if !strings.Contains(a.Status(), "https://shop.example/live") {
		t.Fatalf("expected l to advance to second link URL, got %q", a.Status())
	}

	// y yanks the active link URL; enter opens it through handlers.
	d.Keys("y")
	if reg, _ := a.nav.Registers.Get('"'); reg.Text != "https://shop.example/live" {
		t.Fatalf("yanked link = %q, want https://shop.example/live", reg.Text)
	}
	d.Keys("enter")
	runs, err := a.Store.Query(data.Query{Kind: data.KindRun})
	if err != nil {
		t.Fatalf("query runs: %v", err)
	}
	foundOpen := false
	for _, r := range runs.Items {
		if strings.Contains(r.Text("command"), "https://shop.example/live") || strings.Contains(r.Text("title"), "https://shop.example/live") {
			foundOpen = true
			break
		}
	}
	if !foundOpen {
		t.Fatalf("enter on active link did not dispatch handler run (status=%q)", a.Status())
	}

	// Opt-out panes.interactiveCanvas=false falls back to PageScrollID scrolling.
	d.Keys("esc")
	a.cfg.userSettings[SetInteractiveCanvas] = false
	a.reloadSettings()
	a.viewMemo.gen++
	d.Keys("enter")
	if a.Focus() != views.PageScrollID {
		t.Fatalf("with interactiveCanvas=false, expected focus on %q, got %q", views.PageScrollID, a.Focus())
	}
}

func TestSlimLeftRailInlineStateIconAndOptOut(t *testing.T) {
	a, d := demoLegacy(t)
	d.Keys("1")
	tbl := a.rail()
	if len(tbl.Columns) != 1 || tbl.Columns[0].Key != "rail" {
		t.Fatalf("default left rail should omit State column, got columns %+v", tbl.Columns)
	}
	if len(tbl.Rows) == 0 || tbl.Rows[0].CellIcons["rail"] == "" {
		t.Fatalf("default left rail row should carry inline status icon on rail column: %+v", tbl.Rows)
	}
	if w := a.Shell.State.Layout.Rects["panes.rail"].W; w > 30 {
		t.Fatalf("default left rail width = %d at 120 cols, want slim (<= 30)", w)
	}

	// Opt-out panes.railStateColumn=true restores the separate State column.
	a.cfg.userSettings[SetRailStateColumn] = true
	a.reloadSettings()
	a.viewMemo.gen++
	tblOptOut := a.rail()
	if len(tblOptOut.Columns) != 2 || tblOptOut.Columns[1].Key != "runStatus" {
		t.Fatalf("with railStateColumn=true, expected State column restored, got %+v", tblOptOut.Columns)
	}
}

func TestTOCLivePreviewOnCursorMoveAndOptOut(t *testing.T) {
	a, d := demoLegacy(t)
	pane, ok := a.SelectedPane()
	if !ok {
		t.Fatal("expected selected pane")
	}

	res, err := a.Router.CreatePage(User, mcpui.CreatePageArgs{
		Pane:   pane.ID,
		Slug:   "preview-canvas",
		Title:  "Preview Canvas",
		Pinned: true,
		Root: ui.ViewNode{
			Kind:  ui.KindMarkdown,
			Props: map[string]any{"markdown": "# Preview Heading\n\nLive preview body for cursor hover."},
		},
	})
	if err != nil {
		t.Fatalf("CreatePage: %v", err)
	}

	// Move from left rail to middle rail (site.toc.screen), then step down to Preview Canvas
	// WITHOUT pressing enter. The right panel must immediately preview the canvas while keeping it unread.
	d.Keys("1", "l")
	for i := 0; i < 10 && a.Focus() != tocPrefix+res.ID; i++ {
		d.Keys("j")
	}
	if a.Focus() != tocPrefix+res.ID {
		t.Fatalf("could not focus preview canvas in TOC, got %q", a.Focus())
	}
	frame := d.Frame()
	if !strings.Contains(frame, "Live preview body for cursor hover.") {
		t.Fatalf("moving cursor onto canvas in middle rail did not live-preview its content:\n%s", frame)
	}
	stored, err := a.Store.Get(core.Ref{Kind: data.KindPage, ID: res.ID})
	if err != nil {
		t.Fatalf("Store.Get: %v", err)
	}
	if !stored.IsUnread() {
		t.Fatalf("live-previewing an unread canvas must not mark it viewed before opening")
	}

	// Stepping right with l keeps the previewed canvas in site.content rather than snapping back to Screen.
	d.Keys("l")
	if !strings.Contains(d.Frame(), "Live preview body for cursor hover.") {
		t.Fatalf("pressing l from previewed canvas snapped away from previewed content:\n%s", d.Frame())
	}

	// Opt-out panes.tocLivePreview=false keeps Screen shown until enter opens the canvas.
	d.Keys("h")
	a.panes.activePage[pane.ID] = views.ScreenPageID
	a.cfg.userSettings[SetTOCLivePreview] = false
	a.reloadSettings()
	a.viewMemo.gen++
	d.Send(resize(120, 36))
	if strings.Contains(d.Frame(), "Live preview body for cursor hover.") {
		t.Fatalf("with tocLivePreview=false, moving cursor in TOC should not live-preview:\n%s", d.Frame())
	}
}
