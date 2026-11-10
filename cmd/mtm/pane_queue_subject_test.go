package main

import (
	"strings"
	"testing"

	"ofam.dev/matomeru/internal/actionitems"
)

// With palette.reach off, pane edit on an action item's row resolves the
// item's pane (editSubject). palette.reach, on by default since ochazuke
// v0.119.0, asks for the pane first (the next test): the library reads only
// a focused pane as the subject, not the one an item names.
func TestExplicitPaneEditFromExternalQueueRow(t *testing.T) {
	t.Parallel()
	a, _, row := externalQueuePromptApp(t, false, tabTurns)
	if err := a.r.Settings().Set("palette.reach", false); err != nil {
		t.Fatal(err)
	}
	press(t, a, ":")
	typeText(t, a, "pane edit")
	pressSettle(t, a, "enter")
	fr := frame(a)
	if a.r.FocusID() != "edit-form/name" || !strings.Contains(fr, "Edit pane: matomeru/claude") || strings.Contains(fr, "Edit action-item") {
		t.Fatalf("pane edit from %s: %s\n%s", row, where(a), fr)
	}
	press(t, a, "esc", "q")
	if a.r.FocusID() != row {
		t.Fatalf("edit returned to %s, want %s", where(a), row)
	}
}

// The default (palette.reach on): an item's row is no focused pane, so the
// palette asks which pane (the missing target is a parameter).
func TestExplicitPaneEditFromExternalQueueRowAsksForThePane(t *testing.T) {
	t.Parallel()
	a, _, _ := externalQueuePromptApp(t, false, tabTurns)
	press(t, a, ":")
	typeText(t, a, "pane edit")
	pressSettle(t, a, "enter")
	if a.r.FocusID() != "args-form/id" || strings.Contains(frame(a), "Edit pane:") {
		t.Fatalf("pane edit from an item row: %s\n%s", where(a), frame(a))
	}
}

func TestExplicitPaneEditQueueSubjectDoesNotOverrideAnID(t *testing.T) {
	t.Parallel()
	a, _, _ := externalQueuePromptApp(t, false, tabTurns)
	press(t, a, ":")
	typeText(t, a, "pane edit w2")
	pressSettle(t, a, "enter")
	if fr := frame(a); a.r.FocusID() != "edit-form/name" || !strings.Contains(fr, "Edit pane: shell") {
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
	// The id is asked for in the args form (palette.reach); with it off the
	// request is refused with "id is required: no pane is focused".
	if a.r.FocusID() != "args-form/id" || strings.Contains(fr, "Edit pane:") || strings.Contains(fr, "Edit action-item") {
		t.Fatalf("no-pane edit: %s, status %q\n%s", where(a), a.m.Status, fr)
	}
}
