package main

import (
	"slices"
	"strings"
	"testing"
	"time"

	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/internal/daemon"
)

// storeTokens are the pane and session stores' change tokens as the
// runtime asks them (the bound stores: copies and derivations joined).
func storeTokens(t *testing.T, a *App) (paneToken, sessionToken string) {
	t.Helper()
	read := func(noun string) string {
		n := a.r.Nouns[noun]
		if n == nil || n.Store == nil || n.Store.ChangeToken == nil {
			t.Fatalf("%s's store keeps no change token", noun)
		}
		token, err := n.Store.ChangeToken(ochazuke.Ctx{Runtime: a.r})
		if err != nil {
			t.Fatal(err)
		}
		if token == "" {
			t.Fatalf("%s's store answers an unknown token once the listing is known", noun)
		}
		return token
	}
	return read("pane"), read("session")
}

// listAgain runs panes.refresh over the fake daemon's listing as edit leaves
// it, and settles it.
func listAgain(t *testing.T, a *App, edit func(f *fakePanes)) {
	t.Helper()
	f := a.panesd.(*fakePanes)
	f.mu.Lock()
	edit(f)
	f.mu.Unlock()
	if err := a.reloadPanes(); err != nil {
		t.Fatal(err)
	}
	settle(a, a.r.PendingCommands())
}

// The pane and session stores' change tokens hold across a refresh whose
// listing is the same, and move when anything a read answers from moves:
// a pane's title, state or membership in the listing, a session's name or
// owner, a pane's owner or star, and a copy.
func TestPaneAndSessionChangeTokens(t *testing.T) {
	t.Parallel()
	a := open(t)
	listAgain(t, a, func(*fakePanes) {})
	pane0, session0 := storeTokens(t, a)
	listAgain(t, a, func(*fakePanes) {})
	if p, s := storeTokens(t, a); p != pane0 || s != session0 {
		t.Fatalf("an unchanged listing moved the tokens:\npane %s → %s\nsession %s → %s", pane0, p, session0, s)
	}
	run := func(q ochazuke.Request) {
		t.Helper()
		if _, err := a.r.Execute(q); err != nil {
			t.Fatal(err)
		}
		settle(a, a.r.PendingCommands())
	}
	for _, c := range []struct {
		name          string
		change        func()
		pane, session bool // which token must move
	}{
		{"title", func() { listAgain(t, a, func(f *fakePanes) { f.panes[0].Title = "retitled" }) }, true, false},
		{"state", func() { listAgain(t, a, func(f *fakePanes) { f.panes[0].AgentState = "working" }) }, true, false},
		{"membership", func() {
			listAgain(t, a, func(f *fakePanes) {
				f.panes = append(f.panes, pane{Session: "matomeru", SessionID: f.panes[0].SessionID, Pane: daemon.Pane{ID: "w9", Name: "ninth"}})
			})
		}, true, true},
		{"session name", func() {
			listAgain(t, a, func(f *fakePanes) {
				for i := range f.panes {
					f.panes[i].Session = "renamed-session"
				}
			})
		}, true, true},
		{"session owner", func() {
			id := a.panes[0].SessionID
			run(ochazuke.Request{Noun: "session", Verb: "set", Args: ochazuke.Arguments{"id": id, "props": map[string]any{"project": ""}}})
			run(ochazuke.Request{Noun: "session", Verb: "set", Args: ochazuke.Arguments{"id": id, "props": map[string]any{"owner": "project:" + a.projects[0].ID}}})
		}, true, true},
		{"pane owner", func() {
			run(ochazuke.Request{Noun: "pane", Verb: "set", Args: ochazuke.Arguments{"id": a.panes[0].ID, "props": map[string]any{"owner": "project:" + a.projects[0].ID}}})
		}, true, false},
		{"star", func() {
			run(ochazuke.Request{Noun: "pane", Verb: "set", Args: ochazuke.Arguments{"id": a.panes[0].ID, "props": map[string]any{"starred": true}}})
		}, true, false},
		{"copy", func() {
			if err := a.work.PutCopy("pane", "gone", a.server(), map[string]any{"id": "gone", "name": "gone"}); err != nil {
				t.Fatal(err)
			}
		}, true, true},
	} {
		p0, s0 := storeTokens(t, a)
		c.change()
		p1, s1 := storeTokens(t, a)
		if c.pane && p1 == p0 {
			t.Errorf("%s: the pane token held (%s)", c.name, p0)
		}
		if c.session && s1 == s0 {
			t.Errorf("%s: the session token held (%s)", c.name, s0)
		}
	}
}

// The palette keeps what it computed at open across a panes.refresh whose
// listing is the same (the stores' change tokens say nothing changed): the
// next key runs no guard and reads no store. After a listing that changed,
// the next key runs the guards again over the new listing (ochazuke's
// TestPaletteRedrawReadsNoStore, over mtm's stores). The fixture's verb's
// guard reads the first pane's title from the pane store.
func TestPaletteKeepsItsMemoAcrossAnUnchangedRefresh(t *testing.T) {
	t.Parallel()
	a := open(t)
	listAgain(t, a, func(*fakePanes) {})
	window, title := a.panes[0].ID, a.panes[0].Title
	guards, titled := 0, false
	a.r.DefineGuard("zz-pane-titled", func(r *ochazuke.Runtime, _ ochazuke.KeyEvent) bool {
		guards++
		thing, err := r.Noun("pane").Store.Get(ochazuke.Ctx{Runtime: r}, window)
		titled = err == nil && thing["title"] == title
		return titled
	})
	a.r.Define("zzprobe").Verb("zztour", func(ochazuke.Ctx) ochazuke.Result { return ochazuke.Done(nil) }, "A verb offered while the first pane keeps its title").When("zz-pane-titled")
	reads := 0
	for _, n := range a.r.Nouns {
		if n == nil || n.Store == nil {
			continue
		}
		if get := n.Store.Get; get != nil {
			n.Store.Get = func(c ochazuke.Ctx, key string) (ochazuke.Thing, error) { reads++; return get(c, key) }
		}
		if list := n.Store.List; list != nil {
			n.Store.List = func(c ochazuke.Ctx, q ochazuke.ListQuery) ([]ochazuke.Thing, error) { reads++; return list(c, q) }
		}
		if query := n.Store.Query; query != nil {
			n.Store.Query = func(c ochazuke.Ctx, q ochazuke.ListQuery) (ochazuke.ListPage, error) { reads++; return query(c, q) }
		}
	}
	key := func(k string) (ran, read int) {
		t.Helper()
		g, r := guards, reads
		a.Update(keyMsg(k))
		a.View()
		return guards - g, reads - r
	}
	if ran, _ := key(":"); ran == 0 || !titled {
		t.Fatal("opening the palette ran no guard: the fixture proves nothing")
	}
	// The palette's first reads copy the panes and sessions in (a copy
	// store's backfill writes the copies): the refresh after them sees the
	// copies changed. From then on a refresh changes nothing.
	listAgain(t, a, func(*fakePanes) {})
	key("z")
	listAgain(t, a, func(*fakePanes) {})
	if ran, read := key("z"); ran != 0 || read != 0 {
		t.Fatalf("the key after an unchanged refresh ran %d guards and read the stores %d times", ran, read)
	}
	listAgain(t, a, func(f *fakePanes) { f.panes[0].Title = "retitled" })
	if ran, _ := key("t"); ran == 0 || titled {
		t.Fatalf("the key after a changed listing ran %d guards (the guard saw the old title: %v)", ran, titled)
	}
}

// The task and project stores' change tokens (which the pane's and the
// session's join, their owners reading through them) move on what a read
// answers from: a task's or a project's write, a focus row, the day, and
// the settings a task read applies. A store whose own token is unknown
// stays unknown through docStore's scope.
func TestTaskAndProjectChangeTokens(t *testing.T) {
	t.Parallel()
	a := open(t)
	tokens := func() (task, project string) {
		t.Helper()
		ctx := ochazuke.Ctx{Runtime: a.r}
		task, err := a.r.Nouns["task"].Store.ChangeToken(ctx)
		if err != nil || task == "" {
			t.Fatalf("task token %q %v", task, err)
		}
		project, err = a.r.Nouns["project"].Store.ChangeToken(ctx)
		if err != nil || project == "" {
			t.Fatalf("project token %q %v", project, err)
		}
		return task, project
	}
	day := a.now()
	a.clock = func() time.Time { return day }
	run := func(q ochazuke.Request) {
		t.Helper()
		if _, err := a.r.Execute(q); err != nil {
			t.Fatal(err)
		}
		settle(a, a.r.PendingCommands())
	}
	added, err := a.work.AddTask(a.projects[0].ID, "a task")
	if err != nil {
		t.Fatal(err)
	}
	task := added.ID
	for _, c := range []struct {
		name          string
		change        func()
		task, project bool // which token must move
	}{
		{"task title", func() {
			run(ochazuke.Request{Noun: "task", Verb: "set", Args: ochazuke.Arguments{"id": task, "props": map[string]any{"title": "retitled"}}})
		}, true, false},
		{"project name", func() {
			run(ochazuke.Request{Noun: "project", Verb: "set", Args: ochazuke.Arguments{"id": a.projects[0].ID, "props": map[string]any{"name": "renamed"}}})
		}, true, true},
		{"focus", func() {
			run(ochazuke.Request{Noun: "focus-item", Verb: "add", Args: ochazuke.Arguments{"thing": "task:" + task}})
		}, true, false},
		{"day", func() { day = day.Add(24 * time.Hour) }, true, false},
		{"focus carry", func() {
			if err := a.r.Settings().Set(settingFocusCarry, "none"); err != nil {
				t.Fatal(err)
			}
		}, true, false},
	} {
		t0, p0 := tokens()
		c.change()
		t1, p1 := tokens()
		if c.task && t1 == t0 {
			t.Errorf("%s: the task token held", c.name)
		}
		if c.project && p1 == p0 {
			t.Errorf("%s: the project token held", c.name)
		}
	}
	unknown := a.docStore("zz", ochazuke.Store{Kind: ochazuke.KindCollection, ChangeToken: func(ochazuke.Ctx) (string, error) { return "", nil }})
	if token, err := unknown.ChangeToken(ochazuke.Ctx{Runtime: a.r}); token != "" || err != nil {
		t.Fatalf("an unknown token answers %q (%v) through docStore", token, err)
	}
}

// Both pane pages follow the daemon's listing as before the stores kept
// change tokens: with the Panes page's tree and the pane index (g I) open,
// a pane opened, one retitled and one changing state each show on both
// after the refresh that lists it, and one closed leaves the Panes page.
func TestPanePagesFollowTheListingWithTokens(t *testing.T) {
	t.Parallel()
	a := open(t)
	listAgain(t, a, func(*fakePanes) {})
	press(t, a, "g", "I")
	settle(a, a.r.PendingCommands())
	rows := func() (index, panes string) {
		t.Helper()
		var b strings.Builder
		for _, n := range a.r.Instances["pane-index/panes"].Component.(*ochazuke.Tree).Nodes() {
			b.WriteString(strings.TrimPrefix(n.ID, "pane-index/panes/") + "=" + strings.Join(n.Cells, "|") + "\n")
		}
		var p strings.Builder
		for _, n := range a.pv.tree.Nodes() {
			if q, ok := n.Data.(pane); ok {
				p.WriteString(q.ID + "=" + q.Title + "|" + q.AgentState + "|" + q.label() + "\n")
			}
		}
		return b.String(), p.String()
	}
	for _, c := range []struct {
		name       string
		edit       func(f *fakePanes)
		want, gone string // a row text both pages show, or no longer show
	}{
		{"opened", func(f *fakePanes) {
			f.panes = append(f.panes, pane{Session: f.panes[0].Session, SessionID: f.panes[0].SessionID, Pane: daemon.Pane{ID: "w7", Name: "seventh"}})
		}, "seventh", ""},
		{"retitled", func(f *fakePanes) { f.panes[0].Name, f.panes[0].Title = "", "fresh-title" }, "fresh-title", ""},
		{"state", func(f *fakePanes) { f.panes[0].AgentState = "needs_input" }, "needs", ""},
		{"closed", func(f *fakePanes) { f.panes = slices.DeleteFunc(f.panes, func(p pane) bool { return p.ID == "w7" }) }, "", "seventh"},
	} {
		listAgain(t, a, c.edit)
		index, panes := rows()
		for page, text := range map[string]string{"pane index": index, "Panes page": panes} {
			if c.want != "" && !strings.Contains(text, c.want) {
				t.Errorf("%s: the %s does not show %q:\n%s", c.name, page, c.want, text)
			}
		}
		// The index keeps a closed pane's row (its copy outlives it, as
		// before the tokens); the Panes page drops it.
		if c.gone != "" && strings.Contains(panes, c.gone) {
			t.Errorf("%s: the Panes page still shows %q:\n%s", c.name, c.gone, panes)
		}
	}
}
