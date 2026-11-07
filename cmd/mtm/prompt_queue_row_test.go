package main

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"ofam.dev/matomeru/internal/actionitems"
)

func externalQueuePromptApp(t *testing.T, agent bool, tab int) (*App, *fakePanes, string) {
	t.Helper()
	a, f := screenBoxApp(t)
	if agent {
		f.panes[0].Harness, f.panes[0].AgentState = "codex", "idle"
	}
	it := addItem(t, a, actionitems.Spec{Source: "gh", Kind: "review-request", Key: "queue-pane", Title: "answer the pane", Window: "w1", Session: "matomeru"})
	reload(t, a)
	row := queueRowStart + it.ID
	if err := a.r.SetFocus(row); err != nil {
		t.Fatal(err)
	}
	a.pv.tab = tab
	settle(a, follow(a))
	frame(a)
	return a, f, row
}

func TestExternalQueueRowWritesFromEveryTab(t *testing.T) {
	t.Parallel()
	for _, agent := range []bool{false, true} {
		for tab, name := range tabNames {
			t.Run(fmt.Sprintf("agent=%t/%s", agent, name), func(t *testing.T) {
				t.Parallel()
				a, f, row := externalQueuePromptApp(t, agent, tab)
				pressSettle(t, a, "i")
				fr := frame(a)
				if a.r.FocusID() != promptID || a.r.BaseMode() != "insert" || !strings.Contains(fr, "[send]") || strings.Contains(fr, "loading…") {
					t.Fatalf("i from %s: %s\n%s", row, where(a), fr)
				}
				if selected, _ := a.selectedRow(); selected != row {
					t.Fatalf("i changed selected row to %s, want %s", selected, row)
				}
				typeText(t, a, "reply to review")
				pressSettle(t, a, "enter")
				if !slices.Equal(f.prompts, []string{"submit w1 reply to review"}) {
					t.Fatalf("writes = %q", f.prompts)
				}
				press(t, a, "esc", "esc")
				if a.r.FocusID() != row {
					t.Fatalf("Esc returned to %s, want originating %s", where(a), row)
				}
			})
		}
	}
}

func TestExternalQueueRowSharesThePanesDraft(t *testing.T) {
	t.Parallel()
	a, f, row := externalQueuePromptApp(t, false, tabTurns)
	pressSettle(t, a, "i")
	typeText(t, a, "same pane draft")
	press(t, a, "esc", "esc")
	if err := a.r.SetFocus("pane:matomeru/w1"); err != nil {
		t.Fatal(err)
	}
	settle(a, follow(a))
	pressSettle(t, a, "i")
	if string(a.pb.input.Text) != "same pane draft" || a.pb.owner != "w1" {
		t.Fatalf("pane draft = %q, owner %q", string(a.pb.input.Text), a.pb.owner)
	}
	press(t, a, "esc", "esc")
	if err := a.r.SetFocus("pane:ochazuke/w2"); err != nil {
		t.Fatal(err)
	}
	settle(a, follow(a))
	pressSettle(t, a, "i")
	typeText(t, a, "other pane")
	press(t, a, "esc", "esc")
	if err := a.r.SetFocus(row); err != nil {
		t.Fatal(err)
	}
	settle(a, follow(a))
	pressSettle(t, a, "i")
	if string(a.pb.input.Text) != "same pane draft" {
		t.Fatalf("queue draft = %q", string(a.pb.input.Text))
	}
	pressSettle(t, a, "enter")
	if !slices.Equal(f.prompts, []string{"submit w1 same pane draft"}) || a.promptDraftText("w2") != "other pane" {
		t.Fatalf("writes = %q, other draft = %q", f.prompts, a.promptDraftText("w2"))
	}
}
