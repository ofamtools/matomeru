package main

import (
	"testing"

	"ofam.dev/matomeru/internal/actionitems"
	"ofam.dev/ochazuke"
)

func TestExplicitPaneEditFromExternalQueueRow(t *testing.T) {
	t.Parallel()
	for _, surface := range []string{"Items", "child", "queue"} {
		t.Run(surface, func(t *testing.T) {
			t.Parallel()
			a, f, child := externalQueuePromptApp(t, false, tabTurns)
			a.renamer = &fakeRenamer{panes: f}
			item := a.r.Instances[child].Data.(actionitems.Item)
			row := child
			switch surface {
			case "Items":
				if err := a.r.OpenPage(inboxPage); err != nil {
					t.Fatal(err)
				}
				row = ""
				for _, candidate := range a.inbox.list.Items() {
					if savedRowKey(a.r.Instances[candidate.ID]) == item.ID {
						row = candidate.ID
						break
					}
				}
				if row == "" {
					t.Fatalf("Items has no row for %s", item.ID)
				}
			case "queue":
				if err := a.r.Settings().Set(settingPaneChildren, []string{childKindSubagents}); err != nil {
					t.Fatal(err)
				}
				reload(t, a)
				for _, section := range a.pv.sections {
					for _, node := range section.tree.Nodes() {
						if it, ok := node.Data.(actionitems.Item); ok && it.ID == item.ID {
							row = node.ID
						}
					}
				}
			}
			if err := a.r.SetFocus(row); err != nil {
				t.Fatal(err)
			}
			press(t, a, ":")
			typeText(t, a, "pane edit")
			pressSettle(t, a, "enter")
			editor := a.r.RecordEditor(editFormID)
			if editor == nil || a.r.FocusID() != editFormID+"/name" || editor.Values()["name"] != "claude" {
				t.Fatalf("pane edit from %s: %s\n%s", row, where(a), frame(a))
			}
			// A write proves the form acts on the row's pane, not merely its label.
			press(t, a, "i", "ctrl+a", "ctrl+k")
			typeText(t, a, "row-pane")
			pressSettle(t, a, "esc")
			got := executeA(t, a, "pane", "get", "", ochazuke.Arguments{"id": "w1"}).(map[string]any)
			if got["name"] != "row-pane" {
				t.Fatalf("pane edit wrote %v", got)
			}
			press(t, a, "q")
			if a.r.FocusID() != row {
				t.Fatalf("edit returned to %s, want %s", where(a), row)
			}
		})
	}
}

func TestExplicitPaneEditQueueSubjectDoesNotOverrideAnID(t *testing.T) {
	t.Parallel()
	a, _, _ := externalQueuePromptApp(t, false, tabTurns)
	press(t, a, ":")
	typeText(t, a, "pane edit w2")
	pressSettle(t, a, "enter")
	editor := a.r.RecordEditor(editFormID)
	if editor == nil || a.r.FocusID() != editFormID+"/name" || editor.Values()["name"] != "shell" {
		t.Fatalf("explicit pane id: %s\n%s", where(a), frame(a))
	}
}

func TestExplicitPaneEditQueueWithoutPaneExplainsWhy(t *testing.T) {
	t.Parallel()
	a, _ := panesApp(t)
	it := addItem(t, a, actionitems.Spec{Source: "gh", Kind: "review-request", Key: "no-pane", Title: "outside review"})
	reload(t, a)
	row := queueRowStart + it.ID
	if err := a.r.SetFocus(row); err != nil {
		t.Fatal(err)
	}
	press(t, a, ":")
	typeText(t, a, "pane edit")
	pressSettle(t, a, "enter")
	if a.r.TopModal() != ochazuke.ArgsFormID || a.r.FocusID() != ochazuke.ArgsFormID+"/id" || a.r.RecordEditor(editFormID) != nil {
		t.Fatalf("no-pane edit: %s, status %q\n%s", where(a), a.m.Status, frame(a))
	}
}

func TestExplicitSessionEditFromPaneChildRow(t *testing.T) {
	t.Parallel()
	a, _, _ := externalQueuePromptApp(t, false, tabTurns)
	press(t, a, ":")
	typeText(t, a, "session edit")
	pressSettle(t, a, "enter")
	editor := a.r.RecordEditor(editFormID)
	if editor == nil || a.r.FocusID() != editFormID+"/name" || editor.Values()["name"] != "matomeru" {
		t.Fatalf("session edit from an item row: %s\n%s", where(a), frame(a))
	}
}
