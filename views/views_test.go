package views

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/table"
)

var owner = core.Actor{Kind: "user", ID: "owner"}

// world is a project with everything a Show page relates to it.
type world struct {
	store                              *data.Store
	project, sub1, sub2, leaf          data.Record
	pane, note, about, run, page, asks data.Record
}

func build(t *testing.T) *world {
	t.Helper()
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	mk := func(kind, id string, fields map[string]any) data.Record {
		t.Helper()
		rec, _, err := store.Create(data.CreateInput{Kind: kind, ID: id, Fields: fields}, owner)
		if err != nil {
			t.Fatal(err)
		}
		return *rec
	}
	w := &world{store: store}
	w.project = mk(data.KindTask, "proj", map[string]any{"title": "Matomeru v2", "description": "Rebuild on Ochazuke v2",
		"defaultDir": "/proj/matomeru-next", "priority": "P0", "workStatus": "active"})
	w.sub1 = mk(data.KindTask, "sub1", map[string]any{"title": "Data layer", "parent": w.project.Ref(), "priority": "P1", "workStatus": "done"})
	w.sub2 = mk(data.KindTask, "sub2", map[string]any{"title": "Show pages", "parent": w.project.Ref(), "workStatus": "blocked"})
	w.leaf = mk(data.KindTask, "leaf", map[string]any{"title": "Link cards", "parent": w.sub2.Ref()})
	w.pane = mk(data.KindPane, "w12", map[string]any{"title": "claude: views", "cwd": "/proj/matomeru-next", "pid": 4242,
		"harness": "claude", "runStatus": "waiting", "task": w.project.Ref()})
	w.note = mk(data.KindDocument, "note1", map[string]any{"title": "Design note", "body": "Tasks all the way down.", "task": w.project.Ref()})
	w.about = mk(data.KindDocument, "note2", map[string]any{"title": "Loose jot", "body": "Attached by about."})
	w.run = mk(data.KindRun, "run1", map[string]any{"title": "gate", "command": "go test ./...", "runStatus": "failed", "exitCode": 1, "task": w.project.Ref()})
	w.page = mk(data.KindPage, "page1", map[string]any{"title": "Compare architectures", "slug": "compare", "pageKind": "response",
		"pane": w.pane.Ref(), "task": w.project.Ref(), "viewedAt": data.WriteTime{},
		"root": ui.ViewNode{Kind: ui.KindVStack, ID: "answer", Children: []ui.ViewNode{
			{Kind: ui.KindCard, ID: "opt-1", Scope: "choice", Focusable: true, Title: "Concentric rings"},
		}}})
	w.asks = mk(data.KindPage, "page2", map[string]any{"title": "Questions", "slug": "questions", "pageKind": "questions", "pinned": true,
		"pane": w.pane.Ref(), "task": w.project.Ref(),
		"root": ui.ViewNode{Kind: ui.KindVStack, Children: []ui.ViewNode{
			{Kind: ui.KindCard, ID: "q-1", Scope: "question", Focusable: true, Unread: true, Title: "Keep Saved Panes?"},
		}}})
	if _, _, err := store.Relate(data.RelateInput{From: w.about.Ref(), Kind: data.RelAbout, To: w.project.Ref()}, owner); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Tag(w.project.Ref(), "v2", owner); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pin(w.project.Ref(), owner); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Star(w.project.Ref(), owner); err != nil {
		t.Fatal(err)
	}
	return w
}

func (w *world) schema(kind string) data.Schema {
	s, _ := w.store.Registry().Get(kind)
	return s
}

func (w *world) all(t *testing.T, kind string) []data.Record {
	t.Helper()
	res, err := w.store.Query(data.Query{Kind: kind})
	if err != nil {
		t.Fatal(err)
	}
	return res.Items
}

// frame renders a view the way the terminal would and returns its text.
func frame(t *testing.T, root ui.ViewNode, focusID string, width, height int) string {
	t.Helper()
	if _, err := json.Marshal(root); err != nil {
		t.Fatalf("view is not plain JSON data: %v", err)
	}
	f, _ := ui.Render(root, ui.DefaultDarkTheme(), focusID, width, height)
	return stripANSI(f.ANSI)
}

func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			for i += 2; i < len(s) && (s[i] < 0x40 || s[i] > 0x7e); i++ {
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func wantText(t *testing.T, got string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("frame does not show %q:\n%s", want, got)
			return
		}
	}
}

// wantChain checks the scopes of a context chain, innermost first, and its
// nearest subject: the record a command issued at the focus acts on. A card
// is its own subject; a form or a terminal takes the record around it.
func wantChain(t *testing.T, root ui.ViewNode, focusID string, subject core.Ref, scopes ...string) []core.ContextFrame {
	t.Helper()
	chain := ui.ContextChain(root, focusID, nil)
	got := make([]string, len(chain))
	for i, f := range chain {
		got[i] = f.Scope
	}
	if !reflect.DeepEqual(got, scopes) {
		t.Fatalf("focus %q: scopes = %v, want %v", focusID, got, scopes)
	}
	var nearest *core.Ref
	for _, f := range chain {
		if f.Subject != nil {
			nearest = f.Subject
			break
		}
	}
	if nearest == nil || *nearest != subject {
		t.Fatalf("focus %q: subject = %v, want %v", focusID, nearest, subject)
	}
	if chain[0].NodeID != focusID {
		t.Fatalf("focus %q: innermost frame is node %q", focusID, chain[0].NodeID)
	}
	return chain
}

func TestIndexView(t *testing.T) {
	w := build(t)
	schema := w.schema(data.KindTask)
	tbl := TableFromRecords(schema, w.all(t, data.KindTask))
	root := IndexView(schema, tbl, "idx")
	wantText(t, frame(t, root, "idx.table", 110, 16), "Tasks", "Title", "Status", "Priority", "Matomeru v2", "Data layer", "Link cards", "task:sub2")
	wantChain(t, root, "idx.table", w.project.Ref(), "task", "index", "app")

	// Moving the selection changes the subject, with nothing registered per row.
	tbl.SelectedIdx = 2
	chain := wantChain(t, IndexView(schema, tbl, "idx"), "idx.table", w.sub2.Ref(), "task", "index", "app")
	if chain[0].Facts["workStatus"] != "blocked" || chain[0].Facts[data.ColArchiveStatus] != data.ArchiveActive {
		t.Fatalf("row facts = %v", chain[0].Facts)
	}

	// Filter on any field, shown or not; sort; group.
	tbl.Query, tbl.FilterField, tbl.MatchMode = "^p[01]$", "priority", table.MatchRegex
	tbl.SortField, tbl.SortDir = "priority", table.SortDesc
	if rows := tbl.Filtered(); len(rows) != 2 || rows[0].Ref.ID != "sub1" || rows[1].Ref.ID != "proj" {
		t.Fatalf("filtered and sorted rows = %+v", rows)
	}
	filtered := frame(t, IndexView(schema, tbl, "idx"), "idx.table", 110, 16)
	wantText(t, filtered, "2 of 4", "priority /^p[01]$ (regex)")
	if strings.Contains(filtered, "Link cards") {
		t.Fatalf("filtered frame still shows a filtered-out row:\n%s", filtered)
	}
	tbl.Query, tbl.GroupField = "", "workStatus"
	wantText(t, frame(t, IndexView(schema, tbl, "idx"), "idx.table", 110, 20), "by Status", "blocked (1)", "done (1)")

	// The same two calls render any other noun.
	for kind, wants := range map[string][]string{
		data.KindPane:     {"Panes", "claude: views", "waiting", "task:proj"},
		data.KindRun:      {"Runs", "gate", "failed", "go test ./..."},
		data.KindDocument: {"Documents", "Design note", "Loose jot"},
		data.KindPage:     {"Pages", "Questions", "Compare architectures"},
	} {
		s := w.schema(kind)
		recs := w.all(t, kind)
		view := IndexView(s, TableFromRecords(s, recs), "idx")
		wantText(t, frame(t, view, "idx.table", 110, 12), wants...)
		wantChain(t, view, "idx.table", recs[0].Ref(), kind, "index", "app")
	}

	// An empty list still has the type as its context, so "new" has a target.
	empty := IndexView(w.schema(data.KindExtension), TableFromRecords(w.schema(data.KindExtension), nil), "idx")
	wantChain(t, empty, "idx.table", core.Ref{Kind: data.KindExtension}, "extension", "index", "app")
}

func TestProjectShowPage(t *testing.T) {
	w := build(t)
	paneCard := "show.sec.pane.task.pane:w12"
	root, err := ShowView(w.store, w.project.Ref(), paneCard)
	if err != nil {
		t.Fatal(err)
	}
	out := frame(t, root, paneCard, 150, 70)
	wantText(t, out,
		// header: title, Ref badge, kind, status, star, pin, action hints
		"Matomeru v2", "task:proj", "project", "active", "★ starred", "pinned", "Rebuild on Ochazuke v2",
		"e edit", "s unstar", "a archive", "enter open claude: views",
		// props
		"Props", "Default dir", "/proj/matomeru-next", "Priority", "P0", "Revision",
		// one section per association, with its records
		"Subtasks", "Data layer", "Show pages", "Panes", "claude: views", "Pages", "Compare architectures", "Questions",
		"Documents", "Design note", "Runs", "gate", "Notes", "Loose jot", "Tags", "v2", "Directories", "Context")
	if strings.Contains(out, "Link cards") {
		t.Fatal("a grandchild is listed as a subtask")
	}

	// Heterogeneous cards in one page: each yields its own scope and Ref.
	for id, ref := range map[string]core.Ref{
		"show.sec.task.parent.task:sub1":        w.sub1.Ref(),
		"show.sec.task.parent.task:sub2":        w.sub2.Ref(),
		paneCard:                                w.pane.Ref(),
		"show.sec.ui.page.task.ui.page:page1":   w.page.Ref(),
		"show.sec.ui.page.task.ui.page:page2":   w.asks.Ref(),
		"show.sec.document.task.document:note1": w.note.Ref(),
		"show.sec.run.task.run:run1":            w.run.Ref(),
		"show.sec.rel.about.in.document:note2":  w.about.Ref(),
		"show.sec.rel.tag.out.tag:v2":           {Kind: data.KindTag, ID: "v2"},
	} {
		node := root.Find(id)
		if node == nil || !node.Focusable || node.Kind != ui.KindCard {
			t.Fatalf("no focusable card %q", id)
		}
		chain := wantChain(t, root, id, ref, ref.Kind, "task", "show", "app")
		if chain[0].Subject == nil || *chain[0].Subject != ref {
			t.Fatalf("%s: the card is not its own subject: %v", id, chain[0].Subject)
		}
		if *chain[1].Subject != w.project.Ref() {
			t.Fatalf("%s: page record in chain = %v", id, chain[1].Subject)
		}
	}
	if facts := ui.ContextChain(root, paneCard, nil)[0].Facts; facts["runStatus"] != "waiting" {
		t.Fatalf("pane card facts = %v", facts)
	}
	if !root.Find("show.sec.ui.page.task.ui.page:page2").Unread || root.Find("show.sec.ui.page.task.ui.page:page1").Unread {
		t.Fatal("only the unviewed page should be unread")
	}
	// "enter" on the focused card opens that record's own Show page.
	var open ui.ActionHint
	for _, a := range root.Find("show.header").Actions {
		if a.Key == "enter" {
			open = a
		}
	}
	if want := map[string]any{"kind": "pane", "id": "w12"}; open.Command != CmdOpen || !reflect.DeepEqual(open.Args["ref"], want) {
		t.Fatalf("open hint = %+v", open)
	}
	// Declared associations keep their section when empty.
	if empty := root.Find("show.sec.rel.directory.out"); empty == nil || empty.Badges[0].Text != "0" {
		t.Fatalf("empty Directories section = %+v", empty)
	}

	// Following the link: the pane's Show page, generated by the same code.
	paneRoot, err := ShowView(w.store, w.pane.Ref(), "show.field.task")
	if err != nil {
		t.Fatal(err)
	}
	wantText(t, frame(t, paneRoot, "show.field.task", 150, 50),
		"claude: views", "pane:w12", "waiting", "Pid", "4242", "→ Matomeru v2", "Pages", "Questions", "enter open Matomeru v2")
	// A Ref field is a focusable link card with the target's scope and Ref.
	wantChain(t, paneRoot, "show.field.task", w.project.Ref(), "task", "pane", "show", "app")

	// A subtask links up to its parent and lists its own subtasks.
	subRoot, err := ShowView(w.store, w.sub2.Ref(), "")
	if err != nil {
		t.Fatal(err)
	}
	wantText(t, frame(t, subRoot, "", 150, 50), "Show pages", "blocked", "→ Matomeru v2", "Link cards", "enter open focused relation")
	wantChain(t, subRoot, "show.field.parent", w.project.Ref(), "task", "task", "show", "app")
	wantChain(t, subRoot, "show.sec.task.parent.task:leaf", w.leaf.Ref(), "task", "task", "show", "app")

	// A kind with no schema and a missing record.
	if _, _, err := w.store.Create(data.CreateInput{Kind: "gh.pr", ID: "42", Fields: map[string]any{"title": "Fix flake", "state": "open"}}, owner); err != nil {
		t.Fatal(err)
	}
	prRoot, err := ShowView(w.store, core.Ref{Kind: "gh.pr", ID: "42"}, "")
	if err != nil {
		t.Fatal(err)
	}
	wantText(t, frame(t, prRoot, "", 120, 30), "Fix flake", "gh.pr:42", "State", "open")
	if _, err := ShowView(w.store, core.Ref{Kind: data.KindTask, ID: "nope"}, ""); err == nil {
		t.Fatal("ShowView of a missing record returned no error")
	}
}

func TestEditView(t *testing.T) {
	w := build(t)
	schema := w.schema(data.KindTask)
	fm := FormForRecord(schema, &w.sub1)
	root := EditView(schema, &w.sub1, fm, "edit")
	wantText(t, frame(t, root, "edit.form", 100, 16),
		"Edit Data layer", "task:sub1", "title", "Data layer", "workStatus", "(•) done", "priority", "(•) P1", "parent", "task:proj", "defaultDir", "save")
	chain := wantChain(t, root, "edit.form", w.sub1.Ref(), "form", "edit", "app")
	if chain[0].Facts["field"] != "title" || chain[0].Facts["fieldKind"] != "text" {
		t.Fatalf("form facts = %v", chain[0].Facts)
	}
	// Every schema field is a form field, with the widget its type implies.
	if len(fm.Specs) != len(schema.Fields) {
		t.Fatalf("form has %d fields, schema %d", len(fm.Specs), len(schema.Fields))
	}

	// Submitting gives typed values the store takes as they are.
	fm.SetValue("title", "Data layer and store")
	fm.SetValue("workStatus", "active")
	values, ok := fm.Submit()
	if !ok {
		t.Fatalf("submit rejected: %v", fm.Errors)
	}
	if values["parent"] != w.project.Ref() {
		t.Fatalf("parent value = %#v", values["parent"])
	}
	saved, _, err := w.store.Patch(data.PatchInput{Ref: w.sub1.Ref(), Fields: values, IfRevision: w.sub1.ChangeSetNumber}, owner)
	if err != nil {
		t.Fatal(err)
	}
	if parent, _ := saved.RefField("parent"); saved.Text("title") != "Data layer and store" || saved.Text("workStatus") != "active" || parent != w.project.Ref() {
		t.Fatalf("saved = %v", saved.Fields)
	}

	// A new record: the schema's defaults, and the bare type as context.
	paneSchema := w.schema(data.KindPane)
	blank := FormForRecord(paneSchema, nil)
	newRoot := EditView(paneSchema, nil, blank, "new")
	wantText(t, frame(t, newRoot, "new.form", 100, 14), "New pane", "runStatus", "(•) unknown", "cwd", "pid")
	wantChain(t, newRoot, "new.form", core.Ref{Kind: data.KindPane}, "form", "edit", "app")
}

func tocIDs(root ui.ViewNode) []string {
	var out []string
	for _, entry := range root.Find("site.toc").Children[0].Children {
		out = append(out, entry.ID)
	}
	return out
}

func TestPaneSiteView(t *testing.T) {
	w := build(t)
	// A page made later but pinned, and a plain later page.
	plan, _, _ := w.store.Create(data.CreateInput{Kind: data.KindPage, ID: "page3", Fields: map[string]any{
		"title": "Plan", "slug": "plan", "pageKind": "plan", "pinned": true, "pane": w.pane.Ref(), "viewedAt": data.WriteTime{},
		"root": ui.ViewNode{Kind: ui.KindMarkdown, Props: map[string]any{"markdown": "# Plan\n\n1. data"}}}}, owner)
	pages := []data.Record{w.page, *plan, w.asks}
	screenRef := core.Ref{Kind: data.KindPage, ID: ScreenPageID}

	// Screen is always there and first, then Questions, pinned pages, the rest.
	root := PaneSiteView(w.pane, pages, "", "")
	if got, want := tocIDs(root), []string{"site.toc.screen", "site.toc.page2", "site.toc.page3", "site.toc.page1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("table of contents = %v, want %v", got, want)
	}
	out := frame(t, root, "site.toc.page2", 130, 30)
	wantText(t, out, "Pages", "1 unread", "Screen", "Questions", "Unread", "Plan", "Compare architectures", "live", "terminal  w12")
	questions := root.Find("site.toc.page2")
	if !questions.Unread || !questions.Focusable || questions.Scope != "ui.page" {
		t.Fatalf("questions entry = %+v", questions)
	}
	for _, id := range []string{"site.toc.page1", "site.toc.page3", "site.toc.screen"} {
		if root.Find(id).Unread {
			t.Errorf("%s should not be unread", id)
		}
	}
	// Every entry is a ui.page with its own Ref, inside the pane.
	if entry := wantChain(t, root, "site.toc.page2", w.asks.Ref(), "ui.page", "toc", "pane", "site", "app"); *entry[0].Subject != w.asks.Ref() {
		t.Fatalf("entry subject = %v", entry[0].Subject)
	}
	wantChain(t, root, "site.toc.screen", screenRef, "ui.page", "toc", "pane", "site", "app")
	chain := wantChain(t, root, "site.screen", screenRef, "terminal", "ui.page", "pane", "site", "app")
	if chain[0].Subject != nil || *chain[2].Subject != w.pane.Ref() {
		t.Fatalf("pane in chain = %v", chain[2].Subject)
	}

	// An active page renders its stored tree; its cards keep their own scope.
	root = PaneSiteView(w.pane, pages, "page1", "")
	wantText(t, frame(t, root, "opt-1", 130, 30), "Compare architectures", "Concentric rings", "open")
	if root.Find("site.screen") != nil {
		t.Fatal("the terminal is shown while another page is active")
	}
	chain = ui.ContextChain(root, "opt-1", nil)
	if len(chain) != 5 || chain[0].Scope != "choice" || chain[1].Scope != "ui.page" || *chain[1].Subject != w.page.Ref() || chain[2].Scope != "pane" {
		t.Fatalf("chain inside the page = %+v", chain)
	}
	root = PaneSiteView(w.pane, pages, "page2", "")
	wantText(t, frame(t, root, "q-1", 130, 30), "Keep Saved Panes?")
	if chain = ui.ContextChain(root, "q-1", nil); chain[0].Scope != "question" || *chain[1].Subject != w.asks.Ref() {
		t.Fatalf("chain on the question = %+v", chain)
	}

	// The filter narrows the table of contents; the content stays.
	root = PaneSiteView(w.pane, pages, "page1", "QUE")
	if got := tocIDs(root); !reflect.DeepEqual(got, []string{"site.toc.page2"}) {
		t.Fatalf("filtered table of contents = %v", got)
	}
	wantText(t, frame(t, root, "site.toc.page2", 130, 30), "/QUE", "Questions", "Concentric rings")
	wantText(t, frame(t, PaneSiteView(w.pane, pages, "", "zzz"), "", 130, 30), "no page matches")

	// A stored screen page takes the place of the built-in entry.
	screen, _, _ := w.store.Create(data.CreateInput{Kind: data.KindPage, ID: "scr", Fields: map[string]any{
		"title": "Screen", "slug": "screen", "pageKind": "screen", "pinned": true, "pane": w.pane.Ref()}}, owner)
	root = PaneSiteView(w.pane, append(pages, *screen), "scr", "")
	if got := tocIDs(root); got[0] != "site.toc.scr" || len(got) != 4 || root.Find("site.screen") == nil {
		t.Fatalf("table of contents with a stored screen page = %v", got)
	}

	// The same pages feed a picker, unread first.
	pick := PickerFromRecords(w.store.Registry(), "Pages", pages)
	if items := pick.Filtered(); len(items) != 3 || items[0].Ref.ID != "page2" || !items[0].Unread || items[0].Scope != "ui.page" {
		t.Fatalf("picker items = %+v", items)
	}
}

func TestOutlineView(t *testing.T) {
	w := build(t)
	other, _, _ := w.store.Create(data.CreateInput{Kind: data.KindTask, ID: "proj2", Fields: map[string]any{"title": "Ochazuke v2"}}, owner)
	nodes, err := w.store.TaskTree(false)
	if err != nil {
		t.Fatal(err)
	}
	root := OutlineView(nodes, "leaf", "outline")
	out := frame(t, root, "outline.tree", 90, 14)
	wantText(t, out, "Outline", "2 projects", "5 tasks",
		"[~] Matomeru v2", "project · active P0 ★",
		"├─ ", "[x] Data layer", "done P1",
		"└─ ", "[!] Show pages", "blocked",
		"[ ] Link cards", "todo",
		"[ ] Ochazuke v2", "project · todo")
	if strings.Index(out, "Show pages") > strings.Index(out, "Link cards") || strings.Index(out, "Link cards") > strings.Index(out, "Ochazuke v2") {
		t.Fatalf("outline is not in tree order:\n%s", out)
	}
	tree := root.Find("outline.tree")
	if tree.Kind != ui.KindTree || tree.Props["selected"] != 3 || tree.Props["guides"] != true {
		t.Fatalf("tree props = %v", tree.Props)
	}
	chain := wantChain(t, root, "outline.tree", w.leaf.Ref(), "task", "outline", "app")
	if chain[0].Facts["depth"] != 2 || chain[0].Facts["project"] != false || chain[0].Facts["workStatus"] != "todo" {
		t.Fatalf("selected row facts = %v", chain[0].Facts)
	}
	// A root task is selected as a project; an unknown selection falls to the first row.
	chain = wantChain(t, OutlineView(nodes, "proj2", "outline"), "outline.tree", other.Ref(), "task", "outline", "app")
	if chain[0].Facts["project"] != true {
		t.Fatalf("root row facts = %v", chain[0].Facts)
	}
	wantChain(t, OutlineView(nodes, "gone", "outline"), "outline.tree", w.project.Ref(), "task", "outline", "app")
	wantText(t, frame(t, OutlineView(nil, "", "outline"), "outline.tree", 60, 6), "0 projects")
}
