package app

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/mcpui"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/widgets/pathpicker"
	"ofam.dev/ochazuke/widgets/table"
)

// memFS is a small fixed file system, so the smoke run's path picker shows
// the same thing on every machine.
type memFS map[string][]pathpicker.DirEntry

func (m memFS) ReadDir(dir string) ([]pathpicker.DirEntry, error) {
	es, ok := m[dir]
	if !ok {
		return nil, fmt.Errorf("%s: no such directory", dir)
	}
	return es, nil
}

func (memFS) ReadPreview(string, int) (string, error) { return "", nil }
func (memFS) HomeDir() (string, error)                { return "/home/owner", nil }

func smokeFS() memFS {
	return memFS{
		"/":     {{Name: "home", Path: "/home", IsDir: true}},
		"/home": {{Name: "owner", Path: "/home/owner", IsDir: true}},
		"/home/owner": {
			{Name: "proj", Path: "/home/owner/proj", IsDir: true},
			{Name: "notes.md", Path: "/home/owner/notes.md", Size: 512},
		},
		"/home/owner/proj": {
			{Name: "matomeru-next", Path: "/home/owner/proj/matomeru-next", IsDir: true},
			{Name: "ochazuke-next", Path: "/home/owner/proj/ochazuke-next", IsDir: true},
		},
		"/home/owner/proj/matomeru-next": {{Name: "go.mod", Path: "/home/owner/proj/matomeru-next/go.mod", Size: 1024}},
		"/home/owner/proj/ochazuke-next": {{Name: "go.mod", Path: "/home/owner/proj/ochazuke-next/go.mod", Size: 900}},
	}
}

// NewDemo opens an in-memory store with the demo data and an app over it,
// with no user configuration and a fixed file system for the path picker.
func NewDemo() (*App, error) {
	store, err := data.Open(":memory:")
	if err != nil {
		return nil, err
	}
	if err := SeedDemo(store); err != nil {
		store.Close()
		return nil, err
	}
	a, err := New(store, Options{FS: smokeFS()})
	if err != nil {
		store.Close()
		return nil, err
	}
	// The demo paths ("~/proj/...") are not on this file system.
	return a, nil
}

type smoke struct {
	*Driver
	out    io.Writer
	fails  []string
	frames []string // named frames for --dump
}

func (k *smoke) check(name string, ok bool) {
	status := "ok  "
	if !ok {
		status = "FAIL"
		k.fails = append(k.fails, name)
	}
	a := k.App
	fmt.Fprintf(k.out, "%s %-64s space=%-10s mode=%-9s focus=%s\n", status, name, a.Space(), a.Mode(), a.Focus())
}

func (k *smoke) section(title string) { fmt.Fprintf(k.out, "\n%s\n", title) }

func (k *smoke) has(parts ...string) bool {
	frame := k.Frame()
	for _, p := range parts {
		if !strings.Contains(frame, p) {
			fmt.Fprintf(k.out, "     the frame does not show %q\n", p)
			return false
		}
	}
	return true
}

func (k *smoke) keep(title string) { k.frames = append(k.frames, title+"\n"+k.Frame()) }

// RunSmoke boots the app on an in-memory demo store, renders 120x36 frames
// and drives a fixed sequence of keys through every part of it, printing one
// line per check to out. With dump it also prints the frames it kept. It
// needs no terminal. The error lists the checks that failed.
func RunSmoke(out io.Writer, dump bool) error {
	a, err := NewDemo()
	if err != nil {
		return err
	}
	defer a.Store.Close()
	k := &smoke{Driver: NewDriver(a, 120, 36), out: out}
	store := a.Store
	byTitle := func(kind, title string) data.Record {
		rec, err := store.GetByField(kind, "title", title)
		if err != nil {
			k.check("demo data has "+kind+" "+title, false)
			return data.Record{Kind: kind}
		}
		return *rec
	}
	pane := byTitle(data.KindPane, "claude: wave 5B")
	other := byTitle(data.KindPane, "nvim: core/keymap.go")
	project := byTitle(data.KindTask, "ochazuke-v2")
	pages := func() map[string]mcpui.PageSummary {
		res, err := a.Router.ListPages(mcpui.ListPagesArgs{Pane: pane.ID})
		byslug := map[string]mcpui.PageSummary{}
		if err == nil {
			for _, p := range res.Pages {
				byslug[p.Slug] = p
			}
		}
		return byslug
	}

	k.section("1. the four spaces")
	k.check("starts on Panes with the tab bar", a.Space() == SpacePanes && k.has("1 Panes", "2 Work", "3 Runs", "4 Extensions"))
	k.check("panes: rail, table of contents and the Screen page", k.has("claude: wave 5B", "nvim: core/keymap.go", "Pages", "Screen", "Questions", "Architecture"))
	k.check("the pane's lane shows as one line above its pages, not among them",
		k.has("Lane: Matomeru v2 Step 2  [███▓░░] 3/6 done · 1 running (settings-worker)") && !k.has2("│ Lane"))
	k.keep("Panes")
	k.Keys("2")
	if a.workTasksLayout() == WorkTasksLayoutTree {
		k.check("2 opens Work: the task tree", a.Space() == SpaceWork && a.Focus() == workTable && k.has("Tasks", "ochazuke-v2", "matomeru-v2", "tree", "└─"))
	} else {
		k.check("2 opens Work: the task index", a.Space() == SpaceWork && a.Focus() == workTable && k.has("Tasks", "ochazuke-v2", "matomeru-v2", "Status", "Priority"))
	}
	k.keep("Work")
	k.Keys("3")
	k.check("3 opens Runs: table and detail", a.Space() == SpaceRuns && k.has("Runs", "gate: views", "Command", "go test -p 1 -race ./data", "Output"))
	k.keep("Runs")
	k.Keys("4")
	k.check("4 opens Extensions: health, capabilities and commands", a.Space() == SpaceExtensions && k.has("Extensions", "o-csv", "o-git", "o-lane", "Capabilities", "Commands", "doctor"))
	k.keep("Extensions")
	k.Keys("]")
	k.check("] wraps round to Panes", a.Space() == SpacePanes)
	k.Keys("[", "1")
	k.check("[ goes back a space, 1 returns to Panes", a.Space() == SpacePanes && a.Focus() == railTable)

	k.section("2. the pane's site: an unread question, answered")
	sel, _ := a.SelectedPane()
	k.check("the rail leads with the pane the owner prompted last", sel != nil && sel.ID == pane.ID)
	k.check("the pane has an unread dot and an unread Questions page", k.has("● claude: wave 5B", "Unread") && pages()["questions"].Unread && a.attention(pane.ID))
	k.check("the other pane, whose pages are read, has no dot", !a.attention(other.ID))
	k.Keys("enter")
	k.check("enter goes from the rail to the pane's pages", a.Focus() == tocPrefix+"screen")
	k.Keys("j", "enter")
	questions := pages()["questions"]
	k.check("opening Questions marks the page viewed", !questions.Unread && a.Focus() == "q-live-terminal" && k.has("Attach live terminals in this wave?"))
	k.check("the question is still open, so the pane keeps its dot", a.attention(pane.ID) && len(questions.Questions) == 1 && !questions.Questions[0].Answered)
	k.Keys("a")
	k.check("a opens the answer form", a.Modal() == modalAnswer && a.Mode() == ModeForm && k.has("later", "now", "send answer"))
	k.Keys("space", "ctrl+s")
	questions = pages()["questions"]
	answered := len(questions.Questions) == 1 && questions.Questions[0].Answered && questions.Questions[0].Answer["choice"] == "later"
	k.check("space picks a choice, ctrl+s sends the answer", a.Modal() == "" && answered && !questions.Unread && k.has("answered"))
	k.keep("Questions, answered")
	k.Keys("h", "j", "enter")
	k.check("the response page opens and renders its cards", !pages()["architecture"].Unread && a.Focus() == "arch.opt.thin" && k.has("Thin app, generated views", "Where the four spaces live"))
	k.check("with everything read and answered the dot is gone", !a.attention(pane.ID) && !k.has2("● claude: wave 5B"))

	k.section("3. the terminal: passthrough and the leader")
	k.Keys("h", "g", "g")
	k.check("g g goes to the top of the pages: Screen", a.Focus() == tocPrefix+"screen")
	k.Keys("enter")
	k.check("enter on Screen goes into the terminal", a.Mode() == ModeTerminal && a.Focus() == siteScreen)
	before, _ := store.Get(other.Ref())
	k.Type("ls")
	k.Keys("enter")
	after, _ := store.Get(pane.Ref())
	k.check("typed keys pass through; the line is the owner's prompt", slices.Contains(a.termOf(pane.ID).lines, "❯ ls") && after.Time("promptedAt").After(pane.Time("promptedAt")) && k.has("❯ ls"))
	k.Keys("j", "q", "2")
	k.check("j, q and 2 are typed, not run", a.Mode() == ModeTerminal && a.Space() == SpacePanes && a.termOf(pane.ID).typed == "jq2")
	if _, _, err := store.RecordPaneInput(other.Ref(), core.Actor{Kind: "agent", ID: "claude"}); err != nil {
		return err
	}
	k.Keys("backspace")
	nowOther, _ := store.Get(other.Ref())
	sel, _ = a.SelectedPane()
	k.check("an agent's input to the other pane does not reorder the rail",
		nowOther.Text("promptedAt") == before.Text("promptedAt") && nowOther.Text("lastAgentBy") == "claude" && sel != nil && sel.ID == pane.ID && a.rail().Rows[0].Ref.ID == pane.ID)
	k.Keys("ctrl+a")
	k.check("ctrl+a pushes the leader layer", a.State().Input.Layer != nil && a.State().Input.Layer.Name == "leader")
	k.Keys("?")
	k.check("ctrl+a ? falls through to the normal binding: help", a.Modal() == modalHelp && k.has("ctrl+a d", "terminal.detach"))
	k.keep("Help, from inside the terminal")
	k.Keys("esc")
	k.check("esc closes help, back in the terminal", a.Modal() == "" && a.Mode() == ModeTerminal)
	k.Keys("ctrl+a", "j")
	k.check("the leader leaves motions out (ctrl+a j)", a.Mode() == ModeTerminal && a.Focus() == siteScreen && a.State().Input.Layer == nil)
	k.Keys("ctrl+a", "2")
	k.check("ctrl+a 2 switches to Work from inside the terminal", a.Space() == SpaceWork && a.Mode() == ModeNormal)
	k.Keys("1")
	k.check("back on Panes the terminal is still attached", a.Mode() == ModeTerminal)
	k.Keys("ctrl+a", "esc")
	k.check("ctrl+a esc sends a literal escape to the pane", a.Mode() == ModeTerminal && a.escapes == 1 && a.State().Input.Layer == nil)
	k.Keys("ctrl+a", "z")
	k.check("ctrl+a z zooms: the rail and the pages are hidden", a.zoom && a.Mode() == ModeTerminal && k.has("zoomed") && !k.has2("Pages") && !k.has2("nvim: core/keymap.go"))
	k.keep("Zoomed pane")
	k.Keys("ctrl+a", "z")
	k.check("ctrl+a z again brings them back", !a.zoom && k.has("Pages", "nvim: core/keymap.go"))
	k.Keys("ctrl+a", "d")
	k.check("ctrl+a d detaches", a.Mode() == ModeNormal && a.Focus() == siteScreen)

	k.section("4. Work: Show pages and the back stack")
	k.Keys("2")
	k.check("the index is focused on the first project", a.Focus() == workTable)
	k.Keys("enter")
	shown, _ := a.Showing()
	k.check("enter opens the project's Show page", shown == project.Ref() && a.Focus() == showHeader && k.has("Tasks › ochazuke-v2", "Subtasks", "Props", "project"))
	k.keep("Project Show page")
	k.Keys("j")
	card := a.Focus()
	k.check("j moves from the header to the first related card", strings.HasPrefix(card, "show.sec.task.parent.task:"))
	k.Keys("enter")
	sub, _ := a.Showing()
	k.check("enter on the card opens that record's own Show page", sub.Kind == data.KindTask && sub != project.Ref() && a.Focus() == showHeader && k.has("Tasks › ochazuke-v2 › Tier 0"))
	k.Keys("j", "enter")
	up, _ := a.Showing()
	k.check("its parent link leads back up to the project (a third page)", up == project.Ref() && len(a.history) == 3)
	k.Keys("backspace")
	back, _ := a.Showing()
	k.check("backspace goes back one page, focus where it was", back == sub && a.Focus() == "show.field.parent")
	k.Keys("H")
	back, _ = a.Showing()
	k.check("H goes back again, to the card that was opened", back == project.Ref() && a.Focus() == card)
	k.Keys("L")
	fwd, _ := a.Showing()
	k.check("L goes forward", fwd == sub)
	k.Keys("H", "H")
	_, showing := a.Showing()
	k.check("back to the index, on the same row", !showing && a.Focus() == workTable && k.has("ochazuke-v2", "matomeru-v2"))
	k.Keys("H")
	k.check("going back past the first page stays put", !showing && a.Focus() == workTable)

	k.section("5. edit, undo, redo")
	k.Keys("e")
	k.check("e opens the edit form inferred from the schema", a.Modal() == modalForm && a.Mode() == ModeForm && k.has("Edit ochazuke-v2", "title", "workStatus", "defaultDir"))
	k.Keys("i")
	k.check("i types into the field", a.Mode() == ModeFormEdit)
	k.Type(" rc1")
	k.Keys("enter")
	title := func() string { rec, _ := store.Get(project.Ref()); return rec.Text("title") }
	k.check("leaving the field stages it without writing yet", a.Mode() == ModeForm && title() == "ochazuke-v2")
	k.Keys("j", "j", "l")
	status := func() string { rec, _ := store.Get(project.Ref()); return rec.Text("workStatus") }
	k.check("l picks the next status without writing yet", status() == "active")
	k.keep("Edit form")
	k.Keys("ctrl+s")
	k.check("ctrl+s saves all changes atomically and closes the form", a.Modal() == "" && title() == "ochazuke-v2 rc1" && status() == "blocked" && k.has("ochazuke-v2 rc1", "blocked"))
	k.Keys("u")
	k.check("u undoes the entire submission in one step", title() == "ochazuke-v2" && status() == "active")
	k.Keys("ctrl+r")
	k.check("ctrl+r redoes it", title() == "ochazuke-v2 rc1" && status() == "blocked")
	k.Keys("u")
	k.check("u restores the task", title() == "ochazuke-v2" && k.has("ochazuke-v2") && !k.has2("ochazuke-v2 rc1"))
	k.Keys("n")
	k.check("n opens a new task form", a.Modal() == modalForm && k.has("New task"))
	k.Keys("i")
	k.Type("Write the release note")
	k.Keys("ctrl+s")
	made, err := store.GetByField(data.KindTask, "title", "Write the release note")
	k.check("ctrl+s submits from edit mode and creates the task", a.Modal() == "" && err == nil && made != nil && k.has("Write the release note"))
	k.Keys("u")
	_, err = store.GetByField(data.KindTask, "title", "Write the release note")
	k.check("u takes the new task back", errors.Is(err, data.ErrNotFound))

	k.section("6. one key snoozes and unsnoozes")
	snoozed := func() bool { rec, _ := store.Get(project.Ref()); return rec.IsSnoozed(time.Now()) }
	k.Keys("g", "g")
	k.check("the first row is not snoozed", !snoozed() && a.Focus() == workTable)
	k.Keys("Z")
	k.check("Z snoozes it (facts.snoozed was false)", snoozed() && k.has("snooze ochazuke-v2"))
	k.Keys("Z")
	k.check("Z unsnoozes it (facts.snoozed was true)", !snoozed() && k.has("unsnooze ochazuke-v2"))
	k.Keys("s")
	starred := func() bool { rec, _ := store.Get(project.Ref()); return rec.IsStarred() }
	k.check("s stars", starred())
	k.Keys("s")
	k.check("s unstars", !starred())
	if a.inWorkTaskTree(a.State()) {
		k.Keys("d")
	} else {
		k.Keys("a")
	}
	archived := func() bool { rec, _ := store.Get(project.Ref()); return rec.IsArchived() }
	listed := func() bool {
		return slices.ContainsFunc(a.tables[workTable].Rows, func(r table.Row) bool { return r.ID == data.RefText(project.Ref()) })
	}
	k.check("d/a archives; the row leaves the list", archived() && !listed())
	k.Keys("u")
	k.check("u brings it back", !archived() && listed())
	k.Keys("/")
	k.Type("rail")
	k.check("/ filters on any field as you type", a.Mode() == ModeFilter && len(a.tables[workTable].Filtered()) == 1 && k.has("Pane rail sorted"))
	k.Keys("esc")
	k.check("esc clears the filter", a.Mode() == ModeNormal && a.tables[workTable].Query == "")
	k.Keys("g", "b")
	k.check("g b offers every field to group by", a.Modal() == modalField && k.has("Group by", "(not grouped)", "workStatus"))
	k.Type("workstatus")
	k.Keys("enter")
	k.check("picking one groups the list: status", a.Modal() == "" && a.tables[workTable].GroupField == "workStatus" && k.has("▾ active", "▾ done"))
	k.Keys("g", "b", "enter")
	k.check("g b enter ungroups", a.tables[workTable].GroupField == "")
	if a.inWorkTaskTree(a.State()) {
		k.Keys("o")
		k.check("o in task tree opens inline sibling row in place", a.Mode() == ModeOutlineEdit && a.outlineEdit != nil && a.Modal() == "")
		k.Keys("esc", "g", "s")
		k.Type("title")
		k.Keys("enter")
		k.check("g s sorts by title", a.tables[workTable].SortField == "title" && k.has("Title ▲"))
		k.Keys("o", "o")
		k.check("o twice more: descending, then off", a.tables[workTable].SortField == "")
	} else {
		k.Keys("o")
		k.check("o sorts by the first column", a.tables[workTable].SortField == "title" && k.has("Title ▲"))
		k.Keys("o", "o")
		k.check("o twice more: descending, then off", a.tables[workTable].SortField == "")
	}

	k.section("7. overlays from anywhere")
	k.Keys("T")
	k.check("T opens the outline", a.Modal() == modalOutline && a.Mode() == ModeOutline && k.has("Outline", "2 projects", "▶ ochazuke-v2", "└─"))
	k.keep("Outline")
	k.Keys("esc")
	k.check("esc closes it", a.Modal() == "")
	k.Keys("T", "j", "enter")
	jumped, _ := a.Showing()
	k.check("T j enter jumps to that task's Show page", a.Modal() == "" && a.Space() == SpaceWork && jumped == sub)
	k.Keys("H", "'")
	k.check("' opens the page picker over every pane", a.Modal() == modalPages && a.Mode() == ModePicker && k.has("Pages", "Questions", "Plan: keymap resolution"))
	k.Keys("esc")
	k.check("esc closes it", a.Modal() == "")
	k.Keys("'")
	k.Type("plan")
	k.Keys("enter")
	sel, _ = a.SelectedPane()
	k.check("' plan enter jumps to that pane and page", a.Space() == SpacePanes && sel != nil && sel.ID == other.ID && strings.HasPrefix(a.Focus(), tocPrefix) && k.has("Keymap resolution"))
	k.Keys("?")
	k.check("? lists the bindings that work here", a.Modal() == modalHelp && k.has("Bindings", "enter", "site.page.open", "toc"))
	k.Type("snooze")
	k.check("typing narrows the list: Z is record.snooze here", len(a.pickers[modalHelp].Filtered()) == 1 && k.has("Z", "record.snooze"))
	k.Keys("esc", ":")
	k.check(": opens the command palette", a.Modal() == modalPalette && k.has("Commands", "archived.toggle", "Show or hide archived records"))
	k.Type("space.runs")
	k.Keys("enter")
	k.check("a palette command runs", a.Modal() == "" && a.Space() == SpaceRuns)
	k.Keys(":", "esc")
	k.check("esc closes the palette", a.Modal() == "" && a.Space() == SpaceRuns)
	k.Keys("r")
	runs, _ := store.Query(data.Query{Kind: data.KindRun})
	k.check("r queues the focused run again as a new run", runs.Total == 4 && runs.Items[3].Text("runStatus") == "queued" && runs.Items[3].Text("title") == "gate: data")
	k.Keys("2", "ctrl+o")
	k.check("ctrl+o opens the path picker at the task's directory", a.Modal() == modalPath && a.path != nil && a.path.Err == "" && k.has("/home/owner/proj/ochazuke-next", "go.mod"))
	k.keep("Path picker")
	k.Keys("esc")
	k.check("esc closes it", a.Modal() == "" && a.path == nil)
	k.Keys("ctrl+o", "left")
	k.check("left goes up a directory", a.path != nil && a.path.Dir == "/home/owner/proj" && k.has("matomeru-next/", "ochazuke-next/"))
	k.Type("mato")
	k.Keys("tab")
	dirs, _ := store.Related(project.Ref(), data.RelDirectory, data.Outgoing)
	k.check("typing filters, tab chooses: the directory is attached to the task",
		a.Modal() == "" && len(dirs) == 1 && dirs[0].To == core.Ref{Kind: "directory", ID: "/home/owner/proj/matomeru-next"})
	k.Keys("u")
	dirs, _ = store.Related(project.Ref(), data.RelDirectory, data.Outgoing)
	k.check("u detaches it", len(dirs) == 0)

	k.section("8. lanes, any noun, moving, settings and bindings")
	k.Keys("1", "W")
	sel, _ = a.SelectedPane()
	k.check("W goes to the next pane that waits for the owner", a.Space() == SpacePanes && sel != nil && sel.ID == pane.ID && k.has("wants you"))
	k.Keys("ctrl+a", "L")
	laneRef, _ := a.Showing()
	k.check("ctrl+a L opens the pane's lane: its timeline and sublanes",
		a.Space() == SpaceWork && laneRef.Kind == data.KindLane && k.has("Matomeru v2 Step 2", "Timeline", "◇ decision", "◆ milestone", "Sublanes", "lane-schema"))
	k.keep("Lane Show page")
	k.Keys("g", "n")
	k.check("g n lists every noun with its count", a.Modal() == modalNouns && k.has("Nouns", "Tasks", "Lanes", "Panes", "Pages", "Notes", "Documents", "Schedules", "Occurrences", "Runs", "Extensions"))
	k.Type("lanes")
	k.Keys("enter")
	k.check("picking Lanes opens their index in Work", a.Modal() == "" && a.Space() == SpaceWork && a.browseKind == data.KindLane && a.Focus() == workTable && k.has("Lanes", "settings-worker", "binding-editor", "Status"))
	k.keep("Lanes index")
	k.Keys("n")
	k.check("n makes a record of that noun", a.Modal() == modalForm && k.has("New lane", "laneStatus", "worktree"))
	k.Keys("esc", "e")
	k.check("e edits every prop: the schema's, then star, archive and snooze", a.Modal() == modalForm && k.has("Edit Matomeru v2 Step 2", "agentModel", "starStatus", "archiveStatus", "snoozedUntil"))
	for range len(a.schema(data.KindLane).Fields) {
		k.Keys("j")
	}
	k.Keys("l", "ctrl+s")
	laneRec, _ := store.Get(laneRef)
	k.check("changing starStatus in the form and submitting stars the record", a.Modal() == "" && laneRec != nil && laneRec.IsStarred() && strings.Contains(a.Status(), "saved lane"))
	k.Keys("u", "H")
	laneRec, _ = store.Get(laneRef)
	k.check("u unstars; back from another noun's index is the tasks", laneRec != nil && !laneRec.IsStarred() && a.browseKind == "" && k.has("Tasks", "ochazuke-v2"))
	k.Keys("g", "g", "m")
	k.check("m opens the tasks to move under, with (None / root)", a.Modal() == modalAssign && k.has("Move ochazuke-v2 to", "(None / root)", "matomeru-v2"))
	k.Type("matomeru-v2")
	k.Keys("enter")
	moved, _ := store.Get(project.Ref())
	parent, _ := moved.RefField("parent")
	k.check("picking a task moves the project under it", a.Modal() == "" && parent == byTitle(data.KindTask, "matomeru-v2").Ref() && k.has("moved ochazuke-v2 to matomeru-v2"))
	k.Keys("u")
	moved, _ = store.Get(project.Ref())
	_, hasParent := moved.RefField("parent")
	k.check("u moves it back to the root", !hasParent)
	k.Keys("S")
	k.check("S opens the settings as a form", a.Modal() == modalSettings && a.Mode() == ModeForm && k.has("Settings", "ui.theme", "ui.density", "ui.borderStyle", "show.metadata", "panes.laneProgressBar"))
	k.keep("Settings")
	k.Keys("l")
	k.check("l picks the next theme: applied at once", a.Shell.Theme.Name != defaultThemeName && a.Settings.String(SetTheme, "") == a.Shell.Theme.Name)
	k.Keys("h", "j", "l")
	k.check("the density changes the theme too", a.Shell.Theme.Name == defaultThemeName && a.Shell.Theme.Density == "spacious")
	k.Keys("h", "esc", "1", "S")
	for range 9 {
		k.Keys("j")
	}
	k.Keys("space", "esc")
	k.check("panes.laneProgressBar off hides the bar", !a.Settings.Bool(SetLaneBar, true) && !k.has2("Lane: Matomeru v2 Step 2"))
	k.Keys("S")
	for range 9 {
		k.Keys("j")
	}
	k.Keys("space", "esc", "2")
	k.Keys("B")
	k.check("B opens the binding editor", a.Modal() == modalBindings && a.Mode() == ModeBindings && k.has("Bindings", "Key", "Scope", "Mode", "Command", "Filter"))
	k.Keys("/")
	k.Type("verb:record.star")
	k.Keys("enter")
	k.check("/ verb:record.star narrows it to one binding", a.Mode() == ModeBindings && len(a.bindEd.Filtered()) == 1 && k.has("record.star"))
	k.keep("Binding editor")
	k.Keys("r", "X", "enter")
	k.check("r X enter rebinds it, live", a.Mode() == ModeBindings && len(a.userBindings) == 2 && k.has("rebound X record.star"))
	k.Keys("esc", "X")
	k.check("the new key works at once: X stars", starred())
	k.Keys("s")
	k.check("s still unstars: that is another binding", !starred())
	k.Keys("s")
	k.check("s no longer stars: that binding moved to X", !starred())
	k.Keys("B", "u", "esc", "s")
	k.check("u in the editor takes the rebind back: s stars again", len(a.userBindings) == 0 && starred())
	k.Keys("s")
	k.check("s unstars", !starred())

	k.section("9. the daily driver: yank, macros, scratches, signals, the reader, the inspector")
	k.Keys("2", "g", "g")
	top, _ := a.tables[workTable].FocusedRow()
	topTitle := a.title(*top.Ref)
	k.Keys("y")
	k.check("y lists every value of the focused record, each shown as it is copied", a.Modal() == modalYank && k.has("Yank from "+topTitle, "workStatus", "ref", "json", "markdown"))
	k.keep("Yank picker")
	k.Keys("y")
	k.check("y y yanks the title into the registers and the clipboard", a.Modal() == "" && a.Register(core.RegUnnamed) == topTitle && a.Register(core.RegClipboard) == topTitle && a.live.clipboard == topTitle)
	k.check("\"% is the focused record", a.Register(core.RegPath) == top.ID)
	workPos := func() int {
		if a.inWorkTaskTree(a.State()) {
			return slices.Index(a.visibleTaskIDs(), a.workTreeSel)
		}
		return a.tables[workTable].SelectedIdx
	}
	k.Keys("q", "a", "j", "q")
	_, recording := a.Macros.Recording()
	k.check("q a records the keys until q", !recording && workPos() == 1)
	k.Keys("@", "a", "@", "@")
	k.check("@ a and @ @ replay them", workPos() == 3)
	k.Keys("g", "g", "ctrl+a", "S")
	k.check("ctrl+a S shows the global scratch terminal over the space", a.Modal() == modalScratch && a.Mode() == ModeTerminal && a.Space() == SpaceWork && k.has("Scratch terminal", "global scratch"))
	k.keep("Global scratch")
	k.Type("ls")
	k.Keys("enter", "ctrl+a", "V")
	k.check("ctrl+a V switches to the scratch notes", a.Modal() == modalScratch && k.has("Scratch notes") && strings.HasSuffix(a.ScratchFile(), scratchFileName))
	k.Keys("ctrl+a", "V")
	k.check("ctrl+a V again hides it: the space and the focus are as they were", a.Modal() == "" && a.Space() == SpaceWork && a.Focus() == workTable && a.Mode() == ModeNormal)
	scratches, _ := store.Query(data.Query{Kind: data.KindPane})
	roles := 0
	for _, p := range scratches.Items {
		if r := p.Text("role"); r == data.RoleScratchTerm || r == data.RoleScratchVim {
			roles++
		}
	}
	k.check("the scratches are two pane records with a role, not a noun of their own", roles == 2)

	for _, title := range []string{"Bump trie to 1.2", "Bump layout to 0.9"} {
		if _, _, err := store.Create(data.CreateInput{Kind: data.KindSignal, Fields: map[string]any{"title": title, "source": "github", "author": "dependabot[bot]"}}, seedActor); err != nil {
			return err
		}
	}
	if _, _, err := store.Create(data.CreateInput{Kind: data.KindSignal, Fields: map[string]any{"title": "Review wanted", "source": "github", "author": "kmo"}}, seedActor); err != nil {
		return err
	}
	k.Keys("g", "n")
	k.Type("signals")
	k.Keys("enter")
	k.check("signals are a noun like any other", a.workKind() == data.KindSignal && k.has("Signals", "Bump trie to 1.2", "unreviewed", "R triage.rule.from-signal"))
	k.Keys("R")
	k.check("R drafts a rule from the focused signal", a.Modal() == modalLaunch && k.has("Rule from Bump trie to 1.2", "dependabot[bot]", data.ActionMarkNoise))
	k.keep("Rule from a signal")
	k.Keys("ctrl+s")
	k.check("ctrl+s makes the rule and cleans up the signals like it", a.Modal() == "" && strings.Contains(a.Status(), "2 of 3 unreviewed signals cleaned up") && k.has("Review wanted") && !k.has2("Bump layout"))

	guide, _, err := store.Create(data.CreateInput{Kind: data.KindNote, Fields: map[string]any{"title": "Reader guide",
		"body": "# Reader guide\n\nA note opens here.\n\n## Keys\n\n```sh\nmtm inspect actions\n```\n\nSee [the site](https://ofam.dev/matomeru).\n"}}, seedActor)
	if err != nil {
		return err
	}
	k.Keys("g", "n")
	k.Type("notes")
	k.Keys("enter")
	selectRow(a.tables[workTable], data.RefText(guide.Ref()))
	a.gen++
	k.Keys("o")
	k.check("o opens the note's Markdown in the reader, with its contents", a.Modal() == modalReader && a.Mode() == ModeReader && k.has("Contents", "Reader guide", "Keys", "2 headings"))
	k.keep("Markdown reader")
	k.Keys("y")
	k.check("y in the reader yanks the code block", a.Register(core.RegUnnamed) == "mtm inspect actions")
	k.Keys("q", "O")
	k.check("O offers the handlers that match", a.Modal() == modalOpenWith && k.has("markdown-reader"))
	k.Keys("esc", ":")
	k.Type("inspect actions")
	k.Keys("enter")
	k.check(": inspect actions lists every action with its keys", a.Modal() == modalInspect && a.Mode() == ModeInspect && k.has("Inspect actions", unboundBadge, "a inspect.bind"))
	k.keep("Actions inspector")
	k.Keys("esc", "g", "n")
	k.Type("modes")
	k.Keys("enter")
	k.check("g n lists the modes too", a.Modal() == modalInspect && k.has("Inspect modes", "passthrough", "base normal"))
	k.Keys("esc", "g", "n")
	k.Type("tasks")
	k.Keys("enter")
	k.check("and back to the tasks", a.Modal() == "" && a.workKind() == data.KindTask && a.Focus() == workTable)

	k.section("10. the frame")
	k.Send(resize(120, 36)) // a clean re-render: cached subtrees are reused
	frame := a.Shell.View()
	rows := strings.Split(frame.Content, "\n")
	st := a.Shell.Stats
	k.check("the frame is 120x36 with styling, on the alt screen", len(rows) == 36 && strings.Contains(frame.Content, "\x1b[") && frame.AltScreen)
	k.check("the footer shows the mode, the scope chain and key hints", k.has(" NORMAL · app › work", "enter record.open", "n record.new.here"))
	k.check("unchanged subtrees come from the cache", st.CacheHits > 0)
	k.keep("Final frame")
	fmt.Fprintf(out, "\nframe: %d rows, %d bytes · step %dµs · view %dµs · render %dµs · cache %d hit / %d miss · %d renders\n",
		len(rows), len(frame.Content), st.StepUS, st.LayoutUS, st.RenderUS, st.CacheHits, st.CacheMisses, a.Shell.Renders)
	if dump {
		for _, f := range k.frames {
			title, body, _ := strings.Cut(f, "\n")
			fmt.Fprintf(out, "\n=== %s %s\n", title, strings.Repeat("=", max(0, 114-len(title))))
			for _, r := range strings.Split(body, "\n") {
				fmt.Fprintln(out, strings.TrimRight(r, " "))
			}
		}
	}
	if len(k.fails) > 0 {
		return fmt.Errorf("%d smoke checks failed: %s", len(k.fails), strings.Join(k.fails, "; "))
	}
	fmt.Fprintln(out, "smoke: all checks passed")
	return nil
}

// has2 is has without the report: for checks that a text is absent.
func (k *smoke) has2(part string) bool { return strings.Contains(k.Frame(), part) }
