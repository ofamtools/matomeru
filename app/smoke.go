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
	k.keep("Panes")
	k.Keys("2")
	k.check("2 opens Work: the task index", a.Space() == SpaceWork && a.Focus() == workTable && k.has("Tasks", "ochazuke-v2", "matomeru-v2", "Status", "Priority"))
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
	k.Keys("space", "enter")
	questions = pages()["questions"]
	answered := len(questions.Questions) == 1 && questions.Questions[0].Answered && questions.Questions[0].Answer["choice"] == "later"
	k.check("space picks a choice, enter sends the answer", a.Modal() == "" && answered && !questions.Unread && k.has("answered"))
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
	k.check("ctrl+a ? falls through to the normal binding: help", a.Modal() == modalHelp && k.has("ctrl+a x", "terminal.detach"))
	k.keep("Help, from inside the terminal")
	k.Keys("esc")
	k.check("esc closes help, back in the terminal", a.Modal() == "" && a.Mode() == ModeTerminal)
	k.Keys("ctrl+a", "j")
	k.check("the leader leaves motions out (ctrl+a j)", a.Mode() == ModeTerminal && a.Focus() == siteScreen && a.State().Input.Layer == nil)
	k.Keys("ctrl+a", "2")
	k.check("ctrl+a 2 switches to Work from inside the terminal", a.Space() == SpaceWork && a.Mode() == ModeNormal)
	k.Keys("1")
	k.check("back on Panes the terminal is still attached", a.Mode() == ModeTerminal)
	k.Keys("ctrl+a", "x")
	k.check("ctrl+a x detaches", a.Mode() == ModeNormal && a.Focus() == siteScreen)

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
	k.check("leaving the field writes it: no save key", a.Mode() == ModeForm && title() == "ochazuke-v2 rc1")
	k.Keys("j", "j", "l")
	status := func() string { rec, _ := store.Get(project.Ref()); return rec.Text("workStatus") }
	k.check("l picks the next status and writes it", status() == "blocked")
	k.keep("Edit form")
	k.Keys("esc")
	k.check("esc closes the form; the index shows the change", a.Modal() == "" && k.has("ochazuke-v2 rc1", "blocked"))
	k.Keys("u")
	k.check("u undoes the last change (status)", status() == "active" && title() == "ochazuke-v2 rc1")
	k.Keys("u")
	k.check("u again undoes the one before (title)", title() == "ochazuke-v2")
	k.Keys("ctrl+r")
	k.check("ctrl+r redoes it", title() == "ochazuke-v2 rc1" && status() == "active")
	k.Keys("u")
	k.check("u restores the title", title() == "ochazuke-v2" && k.has("ochazuke-v2") && !k.has2("ochazuke-v2 rc1"))
	k.Keys("n")
	k.check("n opens a new task form", a.Modal() == modalForm && k.has("New task"))
	k.Keys("i")
	k.Type("Write the release note")
	k.Keys("enter", "esc")
	made, err := store.GetByField(data.KindTask, "title", "Write the release note")
	k.check("the first field filled in creates the task", err == nil && made != nil && k.has("Write the release note"))
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
	k.Keys("a")
	archived := func() bool { rec, _ := store.Get(project.Ref()); return rec.IsArchived() }
	listed := func() bool {
		return slices.ContainsFunc(a.tables[workTable].Rows, func(r table.Row) bool { return r.ID == data.RefText(project.Ref()) })
	}
	k.check("a archives; the row leaves the list", archived() && !listed())
	k.Keys("u")
	k.check("u brings it back", !archived() && listed())
	k.Keys("/")
	k.Type("rail")
	k.check("/ filters on any field as you type", a.Mode() == ModeFilter && len(a.tables[workTable].Filtered()) == 1 && k.has("Pane rail sorted"))
	k.Keys("esc")
	k.check("esc clears the filter", a.Mode() == ModeNormal && a.tables[workTable].Query == "")
	k.Keys("B", "B")
	k.check("B groups by the next column: status", a.tables[workTable].GroupField == "workStatus" && k.has("▾ active", "▾ done"))
	for a.tables[workTable].GroupField != "" {
		k.Keys("B")
	}
	k.Keys("o")
	k.check("o sorts by the first column", a.tables[workTable].SortField == "title" && k.has("Title ▲"))
	k.Keys("o", "o")
	k.check("o twice more: descending, then off", a.tables[workTable].SortField == "")

	k.section("7. overlays from anywhere")
	k.Keys("T")
	k.check("T opens the outline", a.Modal() == modalOutline && a.Mode() == ModeOutline && k.has("Outline", "2 projects", "[~] ochazuke-v2", "└─"))
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

	k.section("8. the frame")
	k.Send(resize(120, 36)) // a clean re-render: cached subtrees are reused
	frame := a.Shell.View()
	rows := strings.Split(frame.Content, "\n")
	st := a.Shell.Stats
	k.check("the frame is 120x36 with styling, on the alt screen", len(rows) == 36 && strings.Contains(frame.Content, "\x1b[") && frame.AltScreen)
	k.check("the footer shows the mode, the scope chain and key hints", k.has(" NORMAL · app › work", "enter record.open", "n record.new task"))
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
