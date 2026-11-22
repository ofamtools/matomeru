package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/mcpui"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
)

// seedActor writes the demo data. The agent actor is the pane's agent: its
// pages arrive as an agent's would, through the MCP tools.
var (
	seedActor  = core.Actor{Kind: "system", ID: "mtm.seed", Label: "demo data"}
	seedAgent  = core.Actor{Kind: "agent", ID: "claude", Label: "claude"}
	seedMarker = "o-lane" // the demo is seeded once: this extension says it was
)

// SeedDemo fills a store with a small, realistic v2 data set: two projects
// (root tasks) with subtasks, two panes with pages built through the MCP
// Open UI tools (one open question, one architecture response), notes
// attached by "about", documents, a daily reminder, runs and extensions.
// Seeding a store that already holds the demo changes nothing.
func SeedDemo(store *data.Store) error {
	if _, err := store.GetByField(data.KindExtension, "name", seedMarker); err == nil {
		return nil
	} else if !errors.Is(err, data.ErrNotFound) {
		return err
	}
	var firstErr error
	mk := func(kind string, fields map[string]any) core.Ref {
		rec, _, err := store.Create(data.CreateInput{Kind: kind, Fields: fields}, seedActor)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("seed %s: %w", kind, err)
			}
			return core.Ref{Kind: kind}
		}
		return rec.Ref()
	}
	relate := func(from core.Ref, kind string, to core.Ref) {
		if _, _, err := store.Relate(data.RelateInput{From: from, Kind: kind, To: to}, seedActor); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	task := func(title, status, priority, description string, parent *core.Ref, extra map[string]any) core.Ref {
		fields := map[string]any{"title": title, "workStatus": status, "description": description}
		if priority != "" {
			fields["priority"] = priority
		}
		if parent != nil {
			fields["parent"] = *parent
		}
		for k, v := range extra {
			fields[k] = v
		}
		return mk(data.KindTask, fields)
	}
	at := func(d time.Duration) string { return time.Now().Add(d).UTC().Format(data.TimeFormat) }

	// Two projects: a root task displays as a project.
	ochazuke := task("ochazuke-v2", "active", "P0", "The modal UI kernel: core, ui, tea and the widgets.", nil,
		map[string]any{"defaultDir": "~/proj/ochazuke-next", "repo": "ochazuke", "defaultHarness": "claude"})
	matomeru := task("matomeru-v2", "active", "P0", "Projects, tasks, notes and context beside your panes, rebuilt on Ochazuke v2.", nil,
		map[string]any{"defaultDir": "~/proj/matomeru-next", "repo": "matomeru", "defaultHarness": "claude"})
	task("Tier 0: trie, expr, layout, buffer", "done", "P1", "The pure packages everything else stands on.", &ochazuke, nil)
	kernel := task("Tier 1: core, ui, tea", "done", "P1", "Engine.Step, the ViewNode renderer and the Bubble Tea shell.", &ochazuke, nil)
	task("Leader layer falls through to normal", "done", "P2", "ctrl+a reaches every normal binding from inside a terminal.", &kernel, nil)
	widgets := task("Tier 2: table, picker, pathpicker, form", "active", "P1", "Reusable widget models that render to ViewNodes.", &ochazuke, nil)
	task("Table: group on any column", "todo", "P2", "No list of allowed columns: any cell key groups.", &widgets, nil)
	dataAPI := task("Wave 5A: data store and MCP UI", "done", "P1", "SQLite store, change sets, the four ui.* tools, dynamic views.", &matomeru, nil)
	spacesTask := task("Wave 5B: the four spaces", "active", "P0", "Panes, Work, Runs and Extensions, with overlays from anywhere.", &matomeru, nil)
	task("Pane rail sorted by the owner's prompts", "active", "P1", "Agent input never reorders the rail.", &spacesTask, nil)
	task("Show pages with a browser-style history", "active", "P1", "Enter drills in, backspace goes back, L goes forward.", &spacesTask, nil)
	blocked := task("Attach live terminals", "blocked", "P2", "Needs the PTY host; the Screen page records prompts until then.", &spacesTask, nil)
	task("Reminders on schedules", "todo", "P3", "A reminder is a schedule of kind remind.", &matomeru, nil)

	// Two panes. The first has the newer prompt, so it leads the rail.
	paneViews := mk(data.KindPane, map[string]any{"title": "claude: wave 5B", "cwd": "~/proj/matomeru-next", "pid": 48211, "harness": "claude",
		"runStatus": "waiting", "task": spacesTask, "promptedAt": at(-4 * time.Minute)})
	paneKernel := mk(data.KindPane, map[string]any{"title": "nvim: core/keymap.go", "cwd": "~/proj/ochazuke-next", "pid": 47102, "harness": "nvim",
		"runStatus": "running", "task": kernel, "promptedAt": at(-50 * time.Minute)})
	// An agent typed into the second pane after that; it stays second.
	if _, _, err := store.RecordPaneInput(paneKernel, seedAgent); err != nil && firstErr == nil {
		firstErr = err
	}
	if firstErr != nil {
		return firstErr
	}

	// Pages arrive through the MCP tools, exactly as an agent sends them.
	router := mcpui.New(store)
	call := func(tool string, args any) {
		raw, err := json.Marshal(args)
		if err == nil {
			_, err = router.Call(context.Background(), seedAgent, tool, raw)
		}
		if err != nil && firstErr == nil {
			firstErr = fmt.Errorf("seed %s: %w", tool, err)
		}
	}
	md := func(id, body string) ui.ViewNode {
		return ui.ViewNode{ID: id, Kind: ui.KindMarkdown, Props: map[string]any{"markdown": body}}
	}
	card := func(id, title, summary string, badges ...ui.Badge) ui.ViewNode {
		return ui.ViewNode{ID: id, Kind: ui.KindCard, Scope: "choice", Focusable: true, Border: true, Title: title, Badges: badges,
			Props: map[string]any{"summary": summary}}
	}
	call(mcpui.ToolCreatePage, mcpui.CreatePageArgs{Pane: paneViews.ID, Title: "Architecture: where the spaces live", Slug: "architecture", PageKind: "response",
		Root: ui.ViewNode{Kind: ui.KindVStack, Gap: 1, Children: []ui.ViewNode{
			md("arch.intro", "## Where the four spaces live\n\nThe app is a thin layer: **data** owns records, **views** turns them into ViewNodes, and **app** only wires keys to commands.\n\n- every write is a change set, so every write can be undone\n- a page is data: an agent sends it, the app renders it"),
			{ID: "arch.options", Kind: ui.KindGrid, Gap: 1, MinColW: 34, Children: []ui.ViewNode{
				card("arch.opt.thin", "Thin app, generated views", "Index, Show and Edit come from the schema. A new noun needs no view code.", ui.Badge{Text: "chosen", Tone: "success"}),
				card("arch.opt.pages", "One hand-written page per noun", "More control per page, and a page to write for every noun.", ui.Badge{Text: "rejected"}),
			}},
		}}})
	call(mcpui.ToolAsk, mcpui.AskArgs{Pane: paneViews.ID, ID: "q-live-terminal", Title: "Attach live terminals in this wave?",
		Details: "The Screen page can record prompts now and attach the PTY host later.",
		Choices: []mcpui.Choice{
			{ID: "later", Label: "Record prompts now", Description: "ship the spaces first"},
			{ID: "now", Label: "Attach the PTY host now", Description: "one more dependency in this wave"},
		}})
	call(mcpui.ToolCreatePage, mcpui.CreatePageArgs{Pane: paneKernel.ID, Title: "Plan: keymap resolution", Slug: "plan", PageKind: "plan", Pinned: true,
		Root: ui.ViewNode{Kind: ui.KindVStack, Gap: 1, Children: []ui.ViewNode{
			md("plan.body", "## Keymap resolution\n\n1. capture pass, outer to inner\n2. bubble pass, inner to outer\n3. the any-scope level last\n\nA layer swaps the mode list and filters commands by tag."),
		}}})
	if firstErr != nil {
		return firstErr
	}
	// The owner has read the plan; the response and the question are still unread.
	if plan, err := store.GetByFieldIn(data.KindPage, "slug", "plan", "pane", paneKernel); err == nil {
		if _, err := router.MarkViewed(plan.ID, viewer); err != nil {
			return err
		}
	}

	// Notes are small and attach to any record by "about".
	note := func(title, body string, task core.Ref, about core.Ref) {
		ref := mk(data.KindNote, map[string]any{"title": title, "body": body, "task": task})
		relate(ref, data.RelAbout, about)
	}
	note("Rail order", "Sort by promptedAt only. lastAgentInputAt is shown, never sorted on.", spacesTask, paneViews)
	note("Why one Z key", "Snooze and unsnooze are one key: the binding's When reads facts.snoozed.", spacesTask, spacesTask)
	note("PTY host", "Blocked on the host process; ask again after the wave.", matomeru, blocked)

	// A lane tree: the work an agent does in the first pane, started, logged
	// and attached through the lane tools exactly as an agent would. The pane
	// claims the root lane, so its site shows the lane's progress.
	if err := seedLanes(store, paneViews, spacesTask); err != nil && firstErr == nil {
		firstErr = err
	}

	// Documents are larger and usually files.
	mk(data.KindDocument, map[string]any{"title": "Matomeru v2 design", "filePath": "~/hub/o-plans/matomeru-v2.md",
		"summary": "Schemas, the four spaces and the MCP Open UI tools.", "task": matomeru})
	mk(data.KindDocument, map[string]any{"title": "Ochazuke v2 UX rules", "filePath": "~/proj/ochazuke/docs/UX-RULES.md",
		"summary": "Modes, motions, focus and what a binding may do.", "task": ochazuke})

	// A daily reminder, with one occurrence done and one pending.
	standup := mk(data.KindSchedule, map[string]any{"title": "Review open questions", "scheduleKind": "remind", "cron": "0 9 * * *",
		"nextDueAt": at(18 * time.Hour), "task": matomeru})
	mk(data.KindSchedule, map[string]any{"title": "Nightly gate", "scheduleKind": "run", "cron": "0 2 * * *",
		"command": "go test -p 1 -race ./...", "nextDueAt": at(11 * time.Hour), "task": matomeru})
	mk(data.KindOccurrence, map[string]any{"schedule": standup, "dueAt": at(-6 * time.Hour), "outcomeStatus": "done", "checkedAt": at(-5 * time.Hour)})
	mk(data.KindOccurrence, map[string]any{"schedule": standup, "dueAt": at(18 * time.Hour), "outcomeStatus": "pending"})

	// Runs.
	mk(data.KindRun, map[string]any{"title": "gate: data", "command": "go test -p 1 -race ./data", "cwd": "~/proj/matomeru-next",
		"runStatus": "done", "exitCode": 0, "output": "ok  \tofam.dev/matomeru/data\t1.204s", "task": dataAPI})
	mk(data.KindRun, map[string]any{"title": "gate: views", "command": "go test -p 1 -race ./views", "cwd": "~/proj/matomeru-next",
		"runStatus": "failed", "exitCode": 1, "output": "--- FAIL: TestProjectShowPage (0.01s)\n    views_test.go:231: no focusable card \"show.sec.note.task.note:n1\"\nFAIL\tofam.dev/matomeru/views\t0.412s", "task": dataAPI})
	mk(data.KindRun, map[string]any{"title": "smoke", "command": "go run ./cmd/mtm --smoke", "cwd": "~/proj/matomeru-next",
		"runStatus": "running", "task": spacesTask})

	// Extensions. The marker extension is written last: the demo is whole or absent.
	ext := func(name, health, summary string, capabilities, commands []string) {
		mk(data.KindExtension, map[string]any{"name": name, "binary": "mtm-" + name[2:], "runStatus": health, "summary": summary,
			"capabilities": capabilities, "commands": commands})
	}
	ext("o-csv", "healthy", "Sheets: open, filter and transform CSV files.",
		[]string{"nouns: csv.sheet, csv.row, csv.cell", "page: Sheets", "MCP: csv.sheet.open, csv.row.filter"}, []string{"setup", "doctor"})
	ext("o-git", "degraded", "Repositories, branches, worktrees and diffs.",
		[]string{"nouns: git.repo, git.branch, git.worktree", "page: Git", "MCP: git.branch.list, git.diff-file.show"}, []string{"setup", "doctor", "fetch-all"})
	ext(seedMarker, "healthy", "Lanes: the work agents do for you, with live status.",
		[]string{"nouns: lane.lane, lane.plan", "page: Lanes", "MCP: lane.lane.define, lane.lane.set"}, []string{"setup", "reconcile"})
	return firstErr
}

// seedLanes adds one lane tree through the lane tools: a root lane claimed by
// pane, child lanes in every state the progress bar counts, and a timeline.
func seedLanes(store *data.Store, pane, task core.Ref) error {
	lanes := mcpui.NewLaneTools(store)
	lanes.Getenv = func(string) string { return "" } // the demo never reads the caller's environment
	root, err := lanes.Start(seedAgent, mcpui.LaneStartArgs{Title: "Matomeru v2 Step 2", Status: "running", Task: task.ID, Pane: pane.ID,
		Repo: "matomeru", Branch: "next", Harness: "claude", AgentModel: "opus", Summary: "Rich lanes, the progress bar, settings and the binding editor."})
	if err != nil {
		return fmt.Errorf("seed lane: %w", err)
	}
	for _, child := range []struct{ title, status string }{
		{"lane-schema", "done"}, {"lane-tools", "merged"}, {"progress-bar", "done"},
		{"settings-worker", "running"}, {"binding-editor", "planned"}, {"tutorial", "planned"},
	} {
		if _, err := lanes.Start(seedAgent, mcpui.LaneStartArgs{Title: child.title, Status: child.status, Task: task.ID, ParentLane: root.Lane.ID,
			Branch: "next-" + child.title}); err != nil {
			return fmt.Errorf("seed lane %s: %w", child.title, err)
		}
	}
	for _, entry := range []struct{ kind, text string }{
		{"decision", "lanes and panes are many to many: a claim is a relation"},
		{"milestone", "lane schema and the four lane tools merged"},
		{"verification", "go test -p 1 -race ./data ./mcpui passes"},
		{"blocker", "the binding editor waits for the widget's leader mirror"},
	} {
		if _, err := lanes.Log(seedAgent, mcpui.LaneLogArgs{Lane: root.Lane.ID, Kind: entry.kind, Text: entry.text}); err != nil {
			return fmt.Errorf("seed lane log: %w", err)
		}
	}
	return nil
}
