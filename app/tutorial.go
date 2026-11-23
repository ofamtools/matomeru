package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/mcpui"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
)

// The tutorial is a workspace, not a wizard: its projects, subtasks, lane,
// pane pages, notes and runs are the lessons. Each subtask names the keys to
// try; mark it done (e, then the status) as you go. docs/TUTORIAL.md is the
// same walk on paper.

var tutorialAgent = core.Actor{Kind: "agent", ID: "guide", Label: "tutorial guide"}

// TutorialStep is one lesson: a subtask of the tutorial project.
type TutorialStep struct {
	Title string // what to learn
	Keys  string // the keys to press
	Body  string // what happens
}

// TutorialSteps are the lessons in order. The seeded workspace, the Start
// here page and docs/TUTORIAL.md all come from this one list.
func TutorialSteps() []TutorialStep {
	return []TutorialStep{
		{"Move between the four spaces", "1 2 3 4 · ] [",
			"Panes, Work, Runs and Extensions. The footer always shows the mode, where the focus is, and the keys that work there."},
		{"Move the focus", "j k h l · g g · G · tab",
			"j and k walk everything in drawing order and spill from one list into the next. h and l change column."},
		{"Open a pane's pages", "1 · enter · j · enter",
			"A pane has a site: the live Screen and the pages its agent sent. Enter on the rail goes to the pages, enter on a page opens it."},
		{"Answer the agent's question", "W · a · space · enter",
			"W jumps to the next pane that wants you. The unread dot stays until the question is answered. u takes an answer back."},
		{"Read the lane progress bar", "1 · ctrl+a L",
			"The line above the pane's pages is its lane: done, running and planned parts. ctrl+a L opens the lane with its timeline."},
		{"Type in the terminal", "enter on Screen · ctrl+a d · ctrl+a z · ctrl+a esc",
			"Everything you type goes to the pane. ctrl+a is the leader: d detaches, z zooms, esc sends a real escape, and any normal key works after it."},
		{"Open records and go back", "2 · enter · j · enter · backspace · L",
			"A Show page lists a record's props and everything related to it. Enter drills in, backspace goes back, L goes forward."},
		{"Edit every prop, and undo", "e · i · enter · l · esc · u · ctrl+r",
			"The form has every field: the schema's, star, archive and snooze, and any extra field the record holds. Each change is written as you make it; u undoes."},
		{"Move a record to another task", "m",
			"m picks a task for the focused record: a task moves under it, a note or a run is assigned to it. (None / root) makes a task a project."},
		{"Browse any noun", "g n",
			"Every noun has an index: lanes, notes, documents, schedules, runs. Pick one; enter, e, n and A work the same on all of them."},
		{"Filter, sort and group on any field", "/ · f · g s · g b · o · M",
			"f, g s and g b offer every field of the rows, shown or not. / types the filter, M changes how it matches."},
		{"Change the settings", "S",
			"Theme, density, borders and what the pages show. A change applies at once."},
		{"Rebind a key", "B · / · r · x · R · u",
			"Filter with key: verb: mode: scope: conflict:. r reads new keys, x unbinds, R resets, u undoes. The keymap changes as you go."},
		{"Find everything else", "? · : · T · ' · ctrl+o",
			"? lists the keys that work here, : runs any command, T is the outline, ' picks a page of any pane, ctrl+o picks a path."},
	}
}

// TutorialMarkdown is the guide as Markdown: the Start here page and
// docs/TUTORIAL.md.
func TutorialMarkdown() string {
	var b strings.Builder
	b.WriteString("# Matomeru v2 tutorial\n\n")
	b.WriteString("Run `mtm --tutorial`. It opens a workspace kept in memory: nothing you do there touches your own data or configuration, and it is gone when you quit (`Q`).\n\n")
	b.WriteString("The workspace is the lesson. The project **Learn Matomeru** in Work has one subtask per step below; mark each done as you go (`e`, move to the status, `l`).\n\n")
	for i, st := range TutorialSteps() {
		fmt.Fprintf(&b, "## %d. %s\n\nKeys: `%s`\n\n%s\n\n", i+1, st.Title, st.Keys, st.Body)
	}
	b.WriteString("## What is in the workspace\n\n")
	b.WriteString("- **Panes**: `guide: start here` (a Start here page, an unread question, a lane progress bar) and `shell: try me` (enter on Screen starts a real shell).\n")
	b.WriteString("- **Work**: the projects `Learn Matomeru` and `Sandbox project`, with subtasks, notes and a document.\n")
	b.WriteString("- **Lanes** (`g n`, Lanes): `Tutorial lane` with six child lanes and a timeline of milestones, decisions and a blocker.\n")
	b.WriteString("- **Runs**: three finished runs and a queued one; `n` in Runs starts a real command.\n")
	return b.String()
}

// SeedTutorial fills a store with the tutorial workspace.
func SeedTutorial(store *data.Store) error {
	var firstErr error
	fail := func(what string, err error) {
		if err != nil && firstErr == nil {
			firstErr = fmt.Errorf("tutorial %s: %w", what, err)
		}
	}
	mk := func(kind string, fields map[string]any) core.Ref {
		rec, _, err := store.Create(data.CreateInput{Kind: kind, Fields: fields}, seedActor)
		fail(kind, err)
		if rec == nil {
			return core.Ref{Kind: kind}
		}
		return rec.Ref()
	}
	at := func(d time.Duration) string { return time.Now().Add(d).UTC().Format(data.TimeFormat) }

	learn := mk(data.KindTask, map[string]any{"title": "Learn Matomeru", "workStatus": "active", "priority": "P0",
		"description": "One subtask per lesson. Mark each done as you go: e, move to the status, l."})
	var steps []core.Ref
	for i, st := range TutorialSteps() {
		status := "todo"
		if i == 0 {
			status = "active"
		}
		steps = append(steps, mk(data.KindTask, map[string]any{"title": fmt.Sprintf("%d. %s", i+1, st.Title), "workStatus": status,
			"priority": "P2", "parent": learn, "description": "Keys: " + st.Keys + "\n" + st.Body}))
	}
	sandbox := mk(data.KindTask, map[string]any{"title": "Sandbox project", "workStatus": "todo", "priority": "P3",
		"description": "Yours to break: edit it, move it, archive it, undo it."})
	loose := mk(data.KindTask, map[string]any{"title": "Move me under Learn Matomeru (m)", "workStatus": "todo", "parent": sandbox,
		"description": "Focus this row and press m.", "estimate": 2})
	mk(data.KindTask, map[string]any{"title": "Star me (s), snooze me (Z), archive me (a)", "workStatus": "todo", "parent": sandbox})
	if firstErr != nil {
		return firstErr
	}

	// Two panes. The guide has the newer prompt, so it leads the rail.
	guide := mk(data.KindPane, map[string]any{"title": "guide: start here", "harness": "guide", "runStatus": "waiting", "task": steps[3],
		"promptedAt": at(-2 * time.Minute)})
	shell := mk(data.KindPane, map[string]any{"title": "shell: try me", "harness": "shell", "runStatus": "unknown", "task": steps[5],
		"promptedAt": at(-30 * time.Minute)})
	if firstErr != nil {
		return firstErr
	}

	box := mcpui.NewToolbox(store, nil)
	box.Getenv = func(string) string { return "" }
	call := func(tool string, args any) {
		raw, err := json.Marshal(args)
		if err == nil {
			_, err = box.Call(context.Background(), tutorialAgent, tool, raw)
		}
		fail(tool, err)
	}
	md := func(id, body string) ui.ViewNode {
		return ui.ViewNode{ID: id, Kind: ui.KindMarkdown, Props: map[string]any{"markdown": body}}
	}
	var guideText strings.Builder
	guideText.WriteString("## Start here\n\nThis workspace is kept in memory. Try anything: `u` undoes, and quitting forgets it all.\n\n")
	for i, st := range TutorialSteps() {
		fmt.Fprintf(&guideText, "%d. **%s**: `%s`\n", i+1, st.Title, st.Keys)
	}
	guideText.WriteString("\nThe same steps are subtasks of **Learn Matomeru** in Work (`2`).")
	call(mcpui.ToolCreatePage, mcpui.CreatePageArgs{Pane: guide.ID, Title: "Start here", Slug: "start", PageKind: "plan", Pinned: true,
		Root: ui.ViewNode{Kind: ui.KindVStack, Gap: 1, Children: []ui.ViewNode{md("start.body", guideText.String())}}})
	call(mcpui.ToolAsk, mcpui.AskArgs{Pane: guide.ID, ID: "q-first", Title: "Which part do you want to try first?",
		Details: "An agent asks like this with ui.ask. Press a, choose with space, send with enter.",
		Choices: []mcpui.Choice{
			{ID: "lanes", Label: "Lanes and the progress bar", Description: "ctrl+a L from this pane"},
			{ID: "bindings", Label: "The binding editor", Description: "B from anywhere"},
			{ID: "nouns", Label: "Browse every noun", Description: "g n from anywhere"},
		}})
	call(mcpui.ToolCreatePage, mcpui.CreatePageArgs{Pane: shell.ID, Title: "About this pane", Slug: "about", PageKind: "response",
		Root: ui.ViewNode{Kind: ui.KindVStack, Gap: 1, Children: []ui.ViewNode{md("about.body",
			"## A real shell\n\nGo to **Screen** and press enter: it starts your shell.\n\n- `ctrl+a d` detaches\n- `ctrl+a z` zooms\n- `ctrl+a [` is copy mode\n- `ctrl+a esc` sends escape\n\nThe program sees `MTM_PANE_ID` and `MTM_TASK_ID`: try `echo $MTM_PANE_ID`.")}}})

	// The rich lane: claimed by the guide pane, with a child per state.
	lanes := box.Lanes
	lanes.Getenv = box.Getenv
	root, err := lanes.Start(tutorialAgent, mcpui.LaneStartArgs{Title: "Tutorial lane", Status: "running", Task: learn.ID, Pane: guide.ID,
		Repo: "matomeru", Branch: "tutorial", Harness: "guide", AgentModel: "none", Summary: "The work the guide does for you: watch the bar above its pages."})
	fail("lane", err)
	if root != nil {
		for _, child := range []struct{ title, status string }{
			{"spaces", "done"}, {"pane-site", "done"}, {"lanes", "merged"}, {"editing-worker", "running"}, {"settings", "planned"}, {"bindings", "parked"},
		} {
			_, err := lanes.Start(tutorialAgent, mcpui.LaneStartArgs{Title: child.title, Status: child.status, Task: learn.ID, ParentLane: root.Lane.ID})
			fail("lane "+child.title, err)
		}
		for _, entry := range []struct{ kind, text string }{
			{"milestone", "the workspace was seeded"},
			{"decision", "the tutorial is a workspace, not a wizard"},
			{"verification", "every step is a subtask you can mark done"},
			{"blocker", "waiting for you: answer the question in guide: start here"},
		} {
			_, err := lanes.Log(tutorialAgent, mcpui.LaneLogArgs{Lane: root.Lane.ID, Kind: entry.kind, Text: entry.text})
			fail("lane log", err)
		}
	}

	note := func(title, body string, task, about core.Ref) {
		ref := mk(data.KindNote, map[string]any{"title": title, "body": body, "task": task})
		_, _, err := store.Relate(data.RelateInput{From: ref, Kind: data.RelAbout, To: about}, seedActor)
		fail("note", err)
	}
	note("Notes attach to anything", "This note is about the Learn Matomeru project. N on any record writes one about it.", learn, learn)
	note("Extra fields are kept", "The task under Sandbox project has an estimate no schema declares. Its edit form offers it.", sandbox, loose)
	note("About the guide pane", "A note can be about a pane too.", learn, guide)
	mk(data.KindDocument, map[string]any{"title": "Tutorial (docs/TUTORIAL.md)", "filePath": "docs/TUTORIAL.md",
		"summary": "The same walk on paper.", "body": TutorialMarkdown(), "task": learn})

	mk(data.KindRun, map[string]any{"title": "echo hello", "command": "echo hello", "runStatus": "done", "exitCode": 0, "output": "hello", "task": steps[0]})
	mk(data.KindRun, map[string]any{"title": "a failing check", "command": "false", "runStatus": "failed", "exitCode": 1, "output": "", "task": sandbox})
	mk(data.KindRun, map[string]any{"title": "list files", "command": "ls", "runStatus": "done", "exitCode": 0, "output": "README.md\ndocs", "task": sandbox})
	mk(data.KindRun, map[string]any{"title": "run me again (r)", "command": "date", "runStatus": "queued", "task": steps[0]})
	return firstErr
}

// NewTutorial opens the tutorial: an in-memory store with the tutorial
// workspace and an app over it that reads and writes no user configuration.
// opts.ConfigDir is ignored. The caller closes the app and its Store.
func NewTutorial(opts Options) (*App, error) {
	store, err := data.Open(":memory:")
	if err != nil {
		return nil, err
	}
	if err := SeedTutorial(store); err != nil {
		store.Close()
		return nil, err
	}
	opts.ConfigDir = ""
	a, err := New(store, opts)
	if err != nil {
		store.Close()
		return nil, err
	}
	a.say("tutorial: enter opens the guide's pages · 2 has the steps · nothing here is saved")
	return a, nil
}

// RunTutorialSmoke walks the tutorial workspace without a terminal and
// checks that each mechanic it teaches is there to be tried.
func RunTutorialSmoke(out io.Writer) error {
	a, err := NewTutorial(Options{FS: smokeFS()})
	if err != nil {
		return err
	}
	defer a.Store.Close()
	k := &smoke{Driver: NewDriver(a, 120, 40), out: out}
	k.section("tutorial")
	k.check("starts on Panes with the guide pane, its lane in TOC and an unread question",
		a.Space() == SpacePanes && k.has("guide: start here", "◈ 3/6 Tutorial", "Questions", "Start here") && !k.has2("Lane: Tutorial lane"))
	k.Keys("W")
	k.check("W goes to the open question", a.Space() == SpacePanes && a.Focus() == "q-first" && k.has("Which part do you want to try first?"))
	k.Keys("a", "space", "ctrl+s")
	k.check("a, space, ctrl+s answers it", a.Modal() == "" && k.has("answered"))
	k.Keys("ctrl+a", "L")
	lane, _ := a.Showing()
	k.check("ctrl+a L opens the lane with its timeline", lane.Kind == data.KindLane && k.has("Tutorial lane", "Timeline", "milestone", "Sublanes"))
	k.Keys("2")
	k.Keys("H")
	k.check("Work lists the projects", a.Focus() == workTable && k.has("Learn Matomeru", "Sandbox project"))
	k.Keys("enter")
	k.check("the project's page has one subtask per step", k.has("Subtasks", "1. Move between the four spaces") && len(TutorialSteps()) >= 10)
	k.Keys("g", "n")
	k.check("g n lists every noun", a.Modal() == "nouns" && k.has("Tasks", "Lanes", "Notes", "Runs"))
	k.Keys("esc", "3")
	k.check("Runs has runs to rerun", k.has("echo hello", "run me again (r)"))
	if len(k.fails) > 0 {
		return fmt.Errorf("%d tutorial checks failed: %s", len(k.fails), strings.Join(k.fails, "; "))
	}
	fmt.Fprintln(out, "tutorial: all checks passed")
	return nil
}
