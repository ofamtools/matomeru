package app

import (
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

func TestFormatOutlineSubtreeAndPromptContract(t *testing.T) {
	reg := data.DefaultSchemas()
	taskSchema, _ := reg.Get(data.KindTask)

	root := data.TreeNode{
		Depth: 0,
		Record: data.Record{
			Kind: data.KindTask,
			ID:   "t-root",
			Fields: map[string]any{
				"title":       "Q4 Brain Dump",
				"workStatus":  "active",
				"priority":    "P1",
				"description": "Rough thoughts to triage",
			},
			ArchiveStatus: data.ArchiveActive,
		},
		Children: []data.TreeNode{
			{
				Depth: 1,
				Record: data.Record{
					Kind: data.KindTask,
					ID:   "t-a",
					Fields: map[string]any{
						"title":      "Ship hoisting",
						"workStatus": "todo",
					},
					ArchiveStatus: data.ArchiveActive,
				},
				Children: []data.TreeNode{
					{
						Depth: 2,
						Record: data.Record{
							Kind: data.KindTask,
							ID:   "t-a1",
							Fields: map[string]any{
								"title":      "Write tests",
								"workStatus": "blocked",
								"notes":      "Wait on ochazuke v1.12.0",
							},
							ArchiveStatus: data.ArchiveActive,
						},
					},
				},
			},
		},
	}

	md := formatOutlineSubtree(taskSchema, root)
	for _, want := range []string{
		"- **Q4 Brain Dump** (`task:t-root`, status: `active`, priority: `P1`)",
		"  Description: Rough thoughts to triage",
		"  - Ship hoisting (`task:t-a`, status: `todo`)",
		"    - Write tests (`task:t-a1`, status: `blocked`)",
		"      Notes: Wait on ochazuke v1.12.0",
	} {
		if !strings.Contains(md, want) {
			t.Fatalf("formatOutlineSubtree missing %q:\n%s", want, md)
		}
	}

	pane := data.Record{
		Kind:   data.KindPane,
		ID:     "p-claude",
		Fields: map[string]any{"title": "claude: triage", "harness": "claude", "runStatus": "running"},
	}
	prompt := buildOutlineTriagePrompt(taskSchema, root, pane)
	for _, want := range []string{
		"Q4 Brain Dump",
		"task:t-root",
		"DO NOT modify, create, edit, move, or archive any owner tasks",
		"ui.create_page",
		"ui.page",
		"p-claude",
		"ui.ask",
		"Write tests",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("triage prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestOutlineTriageOneKeyResolutionAndDelivery(t *testing.T) {
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	seed := core.Actor{Kind: "system", ID: "seed"}
	now := time.Now().UTC()

	rootTask, _, _ := store.Create(data.CreateInput{
		Kind:   data.KindTask,
		Fields: map[string]any{"title": "Launch Dump", "workStatus": "todo", "order": int64(1)},
	}, seed)
	childTask, _, _ := store.Create(data.CreateInput{
		Kind:   data.KindTask,
		Fields: map[string]any{"title": "Sub Idea", "parent": rootTask.Ref(), "workStatus": "todo", "order": int64(1)},
	}, seed)

	// Non-agent panes (zsh shell + exited claude) must be ignored.
	shellPane, _, _ := store.Create(data.CreateInput{
		Kind: data.KindPane,
		Fields: map[string]any{
			"title":      "zsh: local",
			"harness":    "zsh",
			"runStatus":  "running",
			"promptedAt": now.Format(data.TimeFormat),
		},
	}, seed)
	_, _, _ = store.Create(data.CreateInput{
		Kind: data.KindPane,
		Fields: map[string]any{
			"title":      "claude: old exited",
			"harness":    "claude",
			"runStatus":  "exited",
			"promptedAt": now.Add(-time.Minute).Format(data.TimeFormat),
		},
	}, seed)

	a, err := New(store, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	enableLegacyPagesForTest(a, SpacePanes)
	d := NewDriver(a, 120, 32)

	// 1. Zero live agent panes -> warns with toast and does NOT open picker.
	d.Keys("T", "X")
	if a.Modal() != "outline" {
		t.Fatalf("with 0 live agent panes, expected modal to remain outline, got %q", a.Modal())
	}
	if !strings.Contains(a.Status(), "launch an agent pane first") {
		t.Fatalf("expected zero-pane guidance status, got %q", a.Status())
	}
	if a.rt.toasts.Len() == 0 {
		t.Fatal("expected warning toast when 0 live agent panes exist")
	}

	// 2. Sole live agent pane -> 1-key X delivers via ComposerModeQueue + confirmation toast.
	solePane, _, _ := store.Create(data.CreateInput{
		Kind: data.KindPane,
		Fields: map[string]any{
			"title":      "claude: sole",
			"harness":    "claude",
			"runStatus":  "running",
			"promptedAt": now.Add(-2 * time.Minute).Format(data.TimeFormat),
		},
	}, seed)
	d.Keys("X")
	queued := a.queuedPromptsFor(solePane.ID)
	if len(queued) != 1 {
		t.Fatalf("sole live pane queued prompts = %d, want 1 (status=%q)", len(queued), a.Status())
	}
	if !strings.Contains(queued[0], "Launch Dump") || !strings.Contains(queued[0], "Sub Idea") || !strings.Contains(queued[0], "ui.create_page") {
		t.Fatalf("delivered triage prompt missing outline/contract:\n%s", queued[0])
	}
	if !strings.Contains(a.Status(), "claude: sole") {
		t.Fatalf("expected confirmation status naming target pane, got %q", a.Status())
	}

	// 3. Two live agent panes + rail selects shellPane -> ambiguous -> opens quick picker!
	secondPane, _, _ := store.Create(data.CreateInput{
		Kind: data.KindPane,
		Fields: map[string]any{
			"title":      "codex: helper",
			"harness":    "codex",
			"runStatus":  "idle",
			"promptedAt": now.Add(-3 * time.Minute).Format(data.TimeFormat),
		},
	}, seed)
	// Ensure rail selects shellPane (most recent promptedAt).
	selectRow(a.rail(), data.RefText(shellPane.Ref()))
	d.Keys("X")
	if a.Modal() != "outline.triage.pane" {
		t.Fatalf("ambiguous >1 live agent panes should open picker, got modal=%q", a.Modal())
	}
	// Move to second item ("codex: helper") and confirm -> delivers to secondPane.
	d.Keys("j", "enter")
	if a.Modal() != "outline" {
		t.Fatalf("picker did not close back to outline: modal=%q", a.Modal())
	}
	if got := a.queuedPromptsFor(secondPane.ID); len(got) != 1 {
		t.Fatalf("picker confirmation did not queue prompt for secondPane: %d", len(got))
	}

	// 4. Selected live agent pane in rail -> 1-key X resolves directly to SelectedPane without picker!
	selectRow(a.rail(), data.RefText(secondPane.Ref()))
	d.Keys("X")
	if a.Modal() != "outline" {
		t.Fatalf("selected live agent pane should not open picker, got modal=%q", a.Modal())
	}
	if got := a.queuedPromptsFor(secondPane.ID); len(got) != 2 {
		t.Fatalf("selected live agent pane queued = %d, want 2", len(got))
	}

	// 5. Ancestor-task-linked live agent pane takes precedence over SelectedPane!
	_, _, err = store.Patch(data.PatchInput{
		Ref:    solePane.Ref(),
		Fields: map[string]any{"task": rootTask.Ref()},
	}, seed)
	if err != nil {
		t.Fatal(err)
	}
	// Unfold Launch Dump, move to childTask, press X -> must pick solePane (linked to ancestor rootTask) over selected secondPane!
	d.Keys("z", "o", "j", "X")
	if om := a.trees.outlineModel(); om.SelectedID != childTask.ID {
		t.Fatalf("expected childTask selected, got %q", om.SelectedID)
	}
	if got := a.queuedPromptsFor(solePane.ID); len(got) != 2 {
		t.Fatalf("ancestor-linked pane queued = %d, want 2 (secondPane=%d)", len(got), len(a.queuedPromptsFor(secondPane.ID)))
	}
	if !strings.Contains(a.queuedPromptsFor(solePane.ID)[1], "Sub Idea") {
		t.Fatalf("child subtree triage prompt missing Sub Idea")
	}

	// 6. Explicit pane arg via command works directly and bypasses precedence.
	d.Keys("esc", "esc")
	d.Keys("2")
	d.Send(core.CommandEvent{Command: core.Command{ID: CmdOutlineTriage, Args: map[string]any{"pane": secondPane.ID}, Actor: User}})
	if got := a.queuedPromptsFor(secondPane.ID); len(got) != 3 {
		t.Fatalf("explicit pane arg queued = %d, want 3 (status=%q)", len(got), a.Status())
	}

	// 7. Pressing X directly in 2 Work tree works too.
	d.Keys("X")
	if got := a.queuedPromptsFor(solePane.ID); len(got) != 3 {
		t.Fatalf("X in 2 Work tree queued = %d, want 3 (status=%q)", len(got), a.Status())
	}
}
