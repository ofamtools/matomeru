package app

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/ext"
	"ofam.dev/matomeru/runs"
)

func TestExtActionsGuardConfirmRefreshAndFeedback(t *testing.T) {
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	runner := runs.New(store, runs.Options{})
	t.Cleanup(func() { runner.Close() })

	dir := t.TempDir()
	stateFile := filepath.Join(dir, "cleaned")
	toolBin := filepath.Join(dir, "o-widget")
	script := `#!/bin/sh
case "$1" in
  query)
    if [ -f "` + stateFile + `" ]; then
      echo '{"total":1,"items":[{"kind":"w.item","id":"w2","fields":{"name":"beta","ready":false}}]}'
    else
      echo '{"total":2,"items":[{"kind":"w.item","id":"w1","fields":{"name":"alpha","ready":true,"size":"400 MB"}},{"kind":"w.item","id":"w2","fields":{"name":"beta","ready":false}}]}'
    fi
    ;;
  purge)
    touch "` + stateFile + `"
    echo "purged $2"
    ;;
esac
`
	if err := os.WriteFile(toolBin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	m, err := ext.ParseManifest([]byte(`{
  "name": "o-widget",
  "schemas": [{
    "kind": "w.item",
    "title": "Widget",
    "plural": "Widgets",
    "summaryField": "name",
    "defaultColumns": ["name", "ready"],
    "fields": [
      {"name": "name", "type": "string"},
      {"name": "ready", "type": "bool"}
    ]
  }],
  "commands": [
    {
      "id": "w.item.purge",
      "description": "Purge ready widget",
      "kinds": ["w.item"],
      "key": "X",
      "keyHint": "purge widget",
      "exec": {
        "argv": ["{{binary}}", "purge", "{{name|id}}"],
        "title": "purge {{name|id}}",
        "refreshKinds": ["w.item"]
      },
      "guard": {
        "when": "fields.ready == true",
        "refuseMessage": "Widget {{name}} is not ready"
      },
      "confirm": {
        "title": "Purge {{name}}",
        "message": "Drop {{size|'data'}} for {{name}}?",
        "label": "Purge",
        "tone": "error"
      },
      "feedback": {
        "running": "Purging {{name}}...",
        "success": "Purged {{name}} ({{size|'data'}})",
        "failure": "Failed {{name}}"
      }
    },
    {
      "id": "w.item.hidden-when-not-ready",
      "description": "Hidden unless ready",
      "kinds": ["w.item"],
      "exec": {"argv": ["echo", "{{name}}"]},
      "guard": {"when": "fields.ready == true", "hideWhenFalse": true}
    }
  ]
}`))
	if err != nil {
		t.Fatal(err)
	}

	a, err := New(store, Options{Runner: runner, FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	d := NewDriver(a, 120, 36)

	if err := a.MountCatalog(toolBin, m); err != nil {
		t.Fatal(err)
	}

	d.Keys("g", "n")
	d.Type("widgets")
	d.Keys("enter")
	a.WaitBackground()
	a.Shell.Update(wakeMsg())
	_ = d.Frame()

	// Row 0 (ready=true): both actions visible in `;` Actions.
	d.Keys(";")
	ids := pickerIDs(a, "record.actions")
	if !slices.Contains(ids, "w.item.purge") || !slices.Contains(ids, "w.item.hidden-when-not-ready") {
		t.Fatalf("expected both actions on ready item, got %v", ids)
	}
	d.Keys("esc")

	// Row 1 (ready=false): HideWhenFalse hides the second action; X refuses with expanded RefuseMessage.
	d.Keys("j", ";")
	ids2 := pickerIDs(a, "record.actions")
	if !slices.Contains(ids2, "w.item.purge") || slices.Contains(ids2, "w.item.hidden-when-not-ready") {
		t.Fatalf("HideWhenFalse not respected on non-ready item: %v", ids2)
	}
	d.Keys("esc", "X")
	if a.Modal() == "confirm" {
		t.Fatal("guard refusal should not open confirm modal")
	}
	if !strings.Contains(a.Status(), "Widget beta is not ready") {
		t.Fatalf("unexpected refusal status %q", a.Status())
	}

	// Row 0 (ready=true): X opens confirm modal with custom tone and expanded message, y runs & refreshes readCache.
	d.Keys("k", "X")
	cm := a.activeConfirm()
	if a.Modal() != "confirm" || cm == nil || cm.View(120, 36).Tone != "error" {
		t.Fatalf("confirm modal = %v (modal=%q)", cm, a.Modal())
	}
	if !strings.Contains(d.Frame(), "Drop 400 MB for alpha?") {
		t.Fatalf("confirm message missing:\n%s", d.Frame())
	}
	d.Keys("y")
	a.WaitBackground()
	a.Shell.Update(wakeMsg())

	// ReadCache refreshed w.item to only 1 item (beta).
	snap, ok := a.readCache.Get("w.item")
	if !ok || len(snap.Items) != 1 || snap.Items[0].ID != "w2" {
		t.Fatalf("readCache not refreshed after purge: ok=%v snap=%+v", ok, snap)
	}
	if frame := d.Frame(); !strings.Contains(frame, "Purged alpha (400 MB)") {
		t.Fatalf("success feedback toast missing:\n%s", frame)
	}
}

func TestVisualSettingsOptOutRebuildsTablesImmediately(t *testing.T) {
	a, d := demo(t)
	d.Keys("2", "g", "n")
	d.Type("lanes")
	d.Keys("enter")
	_ = d.Frame()

	// Default: RowSemanticStyling=true and StatusIcons=true.
	tbl := a.records(workTable, data.KindLane)
	if len(tbl.Rows) == 0 || tbl.Rows[0].Tone == "" || len(tbl.Rows[0].CellIcons) == 0 {
		t.Fatalf("expected styled lane row by default, got %+v", tbl.Rows[0])
	}

	// Toggle ui.rowSemanticStyling=false -> applySettings invalidates recCacheKeys immediately.
	a.userSettings[SetRowSemanticStyling] = false
	a.reloadSettings()
	a.applySettings()
	tblNoRow := a.records(workTable, data.KindLane)
	if tblNoRow.Rows[0].Tone != "" || len(tblNoRow.Rows[0].CellTones) != 0 {
		t.Fatalf("expected empty row/cell tones when ui.rowSemanticStyling=false, got tone=%q cellTones=%v", tblNoRow.Rows[0].Tone, tblNoRow.Rows[0].CellTones)
	}

	// Toggle ui.statusIcons=false -> clears cell icons immediately.
	a.userSettings[SetStatusIcons] = false
	a.reloadSettings()
	a.applySettings()
	tblNoIcons := a.records(workTable, data.KindLane)
	if len(tblNoIcons.Rows[0].CellIcons) != 0 {
		t.Fatalf("expected empty cell icons when ui.statusIcons=false, got %v", tblNoIcons.Rows[0].CellIcons)
	}

	// work.hierarchyLayout = "table" overrides auto default.
	a.userSettings[SetWorkHierarchyLayout] = WorkHierarchyLayoutTable
	a.reloadSettings()
	a.applySettings()
	if a.workTasksLayout() != WorkTasksLayoutTable {
		t.Fatalf("workTasksLayout with work.hierarchyLayout=table = %q", a.workTasksLayout())
	}
}
