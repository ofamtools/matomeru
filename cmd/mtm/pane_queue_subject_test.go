package main

import (
	"strings"
	"testing"

	"ofam.dev/matomeru/internal/actionitems"
)

func TestExplicitPaneEditFromExternalQueueRow(t *testing.T) {
	t.Parallel()
	a, _, row := externalQueuePromptApp(t, false, tabTurns)
	press(t, a, ":")
	typeText(t, a, "pane edit")
	pressSettle(t, a, "enter")
	fr := frame(a)
	if a.r.Focus != "edit-form/name" || !strings.Contains(fr, "Edit pane: matomeru/claude") || strings.Contains(fr, "Edit action-item") {
		t.Fatalf("pane edit from %s: %s\n%s", row, where(a), fr)
	}
	press(t, a, "esc")
	if a.r.Focus != row {
		t.Fatalf("edit returned to %s, want %s", where(a), row)
	}
}

func TestExplicitPaneEditQueueSubjectDoesNotOverrideAnID(t *testing.T) {
	t.Parallel()
	a, _, _ := externalQueuePromptApp(t, false, tabTurns)
	press(t, a, ":")
	typeText(t, a, "pane edit w2")
	pressSettle(t, a, "enter")
	if fr := frame(a); a.r.Focus != "edit-form/name" || !strings.Contains(fr, "Edit pane: shell") {
		t.Fatalf("explicit pane id: %s\n%s", where(a), fr)
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
	_, cmd := a.Update(keyMsg("enter"))
	settle(a, cmd)
	fr := frame(a)
	if a.m.Status != "id is required: no pane is focused" || a.r.Focus != "__palette/line" || strings.Contains(fr, "Edit pane:") || strings.Contains(fr, "Edit action-item") {
		t.Fatalf("no-pane edit: %s, status %q\n%s", where(a), a.m.Status, fr)
	}
}
