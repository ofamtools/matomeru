package migrate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

// writeV1 builds a v1 database: each table is (id TEXT PRIMARY KEY, row TEXT)
// unless its rows are nil, which makes a table of another shape.
func writeV1(t *testing.T, path string, wal bool, tables map[string][][2]string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if wal {
		if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
			t.Fatal(err)
		}
	}
	for name, rows := range tables {
		if rows == nil {
			if _, err := db.Exec("CREATE TABLE " + name + " (a TEXT, b TEXT)"); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if _, err := db.Exec("CREATE TABLE " + name + " (id TEXT PRIMARY KEY, row TEXT)"); err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			if _, err := db.Exec("INSERT INTO "+name+" (id, row) VALUES (?, ?)", r[0], r[1]); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// fileState is what an import must leave as it was: the file's bytes and
// mtime, and the names, sizes and mtimes of everything beside it.
func fileState(t *testing.T, path string, withBytes bool) string {
	t.Helper()
	var b strings.Builder
	if withBytes {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(raw)
		b.WriteString(hex.EncodeToString(sum[:]) + "\n")
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		line := e.Name()
		if !info.IsDir() && (withBytes || e.Name() == filepath.Base(path)) {
			line += fmt.Sprintf(" %s %d", info.ModTime(), info.Size())
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

func openV2(t *testing.T) *data.Store {
	t.Helper()
	s, err := data.Open(filepath.Join(t.TempDir(), "v2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func run(t *testing.T, store *data.Store, from string, apply bool) *Report {
	t.Helper()
	rep, err := Import(context.Background(), store, Options{From: from, Apply: apply})
	if err != nil {
		t.Fatalf("import (apply %v): %v", apply, err)
	}
	return rep
}

// census counts the store's records, outgoing relations and change sets.
func census(t *testing.T, s *data.Store) (records, relations, changeSets int) {
	t.Helper()
	res, err := s.Query(data.Query{IncludeArchived: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, rec := range res.Items {
		rels, err := s.Related(rec.Ref(), "", data.Outgoing)
		if err != nil {
			t.Fatal(err)
		}
		relations += len(rels)
	}
	hist, err := s.History(nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	return len(res.Items), relations, len(hist)
}

func byLegacy(t *testing.T, s *data.Store, kind, legacy string) *data.Record {
	t.Helper()
	rec, err := s.GetByField(kind, "legacyId", legacy)
	if err != nil {
		t.Fatalf("%s: %v", legacy, err)
	}
	return rec
}

func parentOf(t *testing.T, rec *data.Record) core.Ref {
	t.Helper()
	ref, _ := rec.RefField("parent")
	return ref
}

var v1Fixture = map[string][][2]string{
	"projects": {
		{"p1", `{"id":"p1","name":"alpha","repo":"/src/alpha","status":"open","created":1000,"position":2,"settings":{"harness":"claude"},"theme":{"color":"red","glyph":"◆"}}`},
		{"p2", `{"id":"p2","name":"beta","repo":"","status":"closed","created":2000,"priority":"high","settings":{}}`},
	},
	"tasks": {
		{"t1", `{"id":"t1","parent_id":"","project_id":"p1","title":"top","position":1,"status":"active","priority":"P0","created":1100,"updated":1200}`},
		// A task with a project's id: legacy ids are namespaced, so both import.
		{"p1", `{"id":"p1","parent_id":"t1","project_id":"p1","title":"sub","position":1,"status":"done","archived":true,"created":1300}`},
		{"t3", `{"id":"t3","parent_id":"p1","project_id":"p1","title":"subsub","status":"blocked","easy":true,"created":1400}`},
		{"orphan", `{"id":"orphan","parent_id":"gone","project_id":"p1","title":"orphan","position":5,"status":"weird","created":1500}`},
		{"loose", `{"id":"loose","parent_id":"","project_id":"","title":"loose","status":"todo","created":1600}`},
		{"loopA", `{"id":"loopA","parent_id":"loopB","project_id":"p2","title":"loop a","status":"todo","created":1700}`},
		{"loopB", `{"id":"loopB","parent_id":"loopA","project_id":"p2","title":"loop b","status":"todo","created":1800}`},
		{"bad", `{not json`},
	},
	"notes": {
		{"n1", `{"id":"n1","text":"first line\nmore","task_id":"t3","project_id":"p1","created":1}`},
		{"n2", `{"id":"n2","title":"project note","text":"x","project_id":"p2","task_id":"","created":2}`},
		{"n3", `{"id":"n3","text":"floating","created":3}`},
	},
	"directories": {
		{"/src/alpha", `{"path":"/src/alpha","source":"scan","candidate":true}`},
		{"/src/other", `{"path":"/src/other","source":"scan","candidate":true}`},
	},
	"relations": {
		{"d1", `{"key":"k1","kind":"directory","from":"project:p1","to":"directory:/src/alpha-wt","data":{"default":true,"kind":"clone","branch":"main"},"position":1024}`},
		{"d2", `{"key":"k2","kind":"directory","from":"project:p1","to":"directory:/src/alpha","position":2048}`},
		{"d3", `{"key":"k3","kind":"directory","from":"","former_from":"project:p2","to":"directory:/src/beta","position":1024}`},
		{"r1", `{"key":"k4","kind":"relation","from":"note:n2","to":"project:p1","data":{"x":1},"position":1024}`},
		{"r2", `{"key":"k5","kind":"about","from":"pane:abc","to":"question.question:q1","position":1024}`},
		{"r3", `{"key":"k6","kind":"relation","from":"note:n1","to":"note:n1","archived":true,"position":2048}`},
	},
	"schedule_defs": {
		{"s1", `{"id":"s1","title":"sync","interval":300,"timeout":60,"run":{"kind":"shell","text":"make sync"},"created":10}`},
	},
	"reminder_defs": {
		{"r1", `{"id":"r1","title":"meds","rules":[{"from":"2026-11-05","time":"10:30","days":["sun","mon","tue","wed","thu","fri","sat"],"enabled":true,"archived":false}]}`},
	},
}

func TestImport(t *testing.T) {
	src := filepath.Join(t.TempDir(), "mtm.db")
	writeV1(t, src, false, v1Fixture)
	before := fileState(t, src, true)

	// A dry run needs no v2 database.
	dry := run(t, nil, src, false)
	if !dry.DryRun || dry.Applied || dry.Projects.Created != 2 || dry.Tasks.Created != 7 || dry.Tasks.Skipped != 1 || dry.Notes.Created != 3 ||
		dry.NoteAbout.Created != 2 || dry.Directories != (Counts{Total: 4, Created: 2, Skipped: 2}) || dry.Schedules.Created != 2 ||
		dry.Relations != (Counts{Total: 3, Created: 1, Skipped: 2}) || dry.Writes != 0 {
		t.Fatalf("nil-store dry run: %+v", dry)
	}

	// A dry run with a store reports the same and writes nothing.
	store := openV2(t)
	if rep := run(t, store, src, false); rep.Tasks != dry.Tasks || rep.Relations != dry.Relations {
		t.Fatalf("store dry run differs: %+v", rep)
	}
	if recs, rels, sets := census(t, store); recs+rels+sets != 0 {
		t.Fatalf("dry run wrote: %d records, %d relations, %d change sets", recs, rels, sets)
	}

	rep := run(t, store, src, true)
	if !rep.Applied || rep.Projects != dry.Projects || rep.Tasks != dry.Tasks || rep.Notes != dry.Notes || rep.Directories != dry.Directories ||
		rep.Schedules != dry.Schedules || rep.Relations != dry.Relations || rep.Skipped != 5 || rep.Writes == 0 {
		t.Fatalf("apply: %+v", rep)
	}
	for _, want := range []string{"tasks \"bad\": malformed", "parent task gone missing", "status \"weird\" unknown", "task:loose: no project", "parent loop broken", "detached in v1", "picker candidates", "an end was not imported", "archived in v1"} {
		if !slices.ContainsFunc(rep.Warnings, func(w string) bool { return strings.Contains(w, want) }) {
			t.Errorf("no warning %q in %q", want, rep.Warnings)
		}
	}

	alpha, beta := byLegacy(t, store, data.KindTask, "project:p1"), byLegacy(t, store, data.KindTask, "project:p2")
	if _, ok := alpha.RefField("parent"); ok || alpha.Text("title") != "alpha" || alpha.Text("defaultDir") != "/src/alpha-wt" ||
		alpha.Text("repo") != "/src/alpha" || alpha.Text("defaultHarness") != "claude" || alpha.IsArchived() || alpha.Text("workStatus") != "todo" {
		t.Errorf("project alpha: %+v", alpha)
	}
	if !beta.IsArchived() || beta.Text("workStatus") != "done" || beta.Text("priority") != "P1" || beta.Text("defaultDir") != "" {
		t.Errorf("project beta: %+v", beta)
	}
	t1, sub, t3 := byLegacy(t, store, data.KindTask, "task:t1"), byLegacy(t, store, data.KindTask, "task:p1"), byLegacy(t, store, data.KindTask, "task:t3")
	if parentOf(t, t1) != alpha.Ref() || t1.Text("workStatus") != "active" || t1.Text("priority") != "P0" || t1.Text("legacyCreatedAt") != time.UnixMilli(1100).UTC().Format(data.TimeFormat) {
		t.Errorf("t1: %+v", t1)
	}
	if parentOf(t, sub) != t1.Ref() || sub.Text("workStatus") != "done" || !sub.IsArchived() || sub.Ref() == alpha.Ref() {
		t.Errorf("sub: %+v", sub)
	}
	if parentOf(t, t3) != sub.Ref() || t3.Text("workStatus") != "blocked" || !strings.Contains(t3.Text("legacyExtra"), `"easy":true`) {
		t.Errorf("t3: %+v", t3)
	}
	if o := byLegacy(t, store, data.KindTask, "task:orphan"); parentOf(t, o) != alpha.Ref() || o.Text("workStatus") != "todo" || o.Text("legacyStatus") != "weird" {
		t.Errorf("orphan: %+v", o)
	}
	if l := byLegacy(t, store, data.KindTask, "task:loose"); parentOf(t, l) != (core.Ref{}) {
		t.Errorf("loose task has parent %v", parentOf(t, l))
	}
	loopA, loopB := byLegacy(t, store, data.KindTask, "task:loopA"), byLegacy(t, store, data.KindTask, "task:loopB")
	if parentOf(t, loopA) != beta.Ref() || parentOf(t, loopB) != loopA.Ref() {
		t.Errorf("loop: a under %v, b under %v", parentOf(t, loopA), parentOf(t, loopB))
	}
	tree, err := store.TaskTree(true)
	if err != nil {
		t.Fatal(err)
	}
	if len(tree) != 3 || tree[0].Record.ID != alpha.ID || tree[0].Children[0].Record.ID != t1.ID || tree[0].Children[0].Children[0].Children[0].Record.ID != t3.ID {
		t.Errorf("task tree roots: %d", len(tree))
	}

	n1, n2, n3 := byLegacy(t, store, data.KindNote, "note:n1"), byLegacy(t, store, data.KindNote, "note:n2"), byLegacy(t, store, data.KindNote, "note:n3")
	for _, c := range []struct {
		note  *data.Record
		about core.Ref
	}{{n1, t3.Ref()}, {n2, beta.Ref()}} {
		about, err := store.Related(c.note.Ref(), data.RelAbout, data.Outgoing)
		if task, _ := c.note.RefField("task"); err != nil || len(about) != 1 || about[0].To != c.about || task != c.about {
			t.Errorf("note %s about %v (%v), want %v", c.note.Text("legacyId"), about, err, c.about)
		}
	}
	if n1.Text("title") != "first line" || n1.Text("body") != "first line\nmore" {
		t.Errorf("n1: %+v", n1)
	}
	if rels, _ := store.Related(n3.Ref(), "", data.Both); len(rels) != 0 {
		t.Errorf("unattached note has relations %v", rels)
	}
	if rels, _ := store.Related(n2.Ref(), "relation", data.Outgoing); len(rels) != 1 || rels[0].To != alpha.Ref() || rels[0].Meta["x"] != float64(1) || rels[0].Meta["legacyKey"] != "k4" {
		t.Errorf("note relation: %+v", rels)
	}
	dirs, _ := store.Related(alpha.Ref(), data.RelDirectory, data.Outgoing)
	if len(dirs) != 2 || dirs[0].To != (core.Ref{Kind: KindDirectory, ID: "/src/alpha-wt"}) || dirs[0].Meta["default"] != true || dirs[0].Position != 1024 || dirs[1].To.ID != "/src/alpha" {
		t.Errorf("directories: %+v", dirs)
	}

	meds, sync := byLegacy(t, store, data.KindSchedule, "reminder:r1"), byLegacy(t, store, data.KindSchedule, "schedule:s1")
	if meds.Text("scheduleKind") != "remind" || meds.Text("cron") != "30 10 * * *" || !strings.Contains(meds.Text("rules"), `"time":"10:30"`) {
		t.Errorf("reminder: %+v", meds)
	}
	if sync.Text("scheduleKind") != "run" || sync.Text("command") != "make sync" || sync.Text("intervalSeconds") != "300" {
		t.Errorf("schedule: %+v", sync)
	}

	// Again: nothing new, nothing written, in a dry run or an apply.
	recs, rels, sets := census(t, store)
	for _, apply := range []bool{false, true} {
		again := run(t, store, src, apply)
		if again.Projects.Unchanged != 2 || again.Tasks.Unchanged != 7 || again.Notes.Unchanged != 3 || again.NoteAbout.Unchanged != 2 ||
			again.Directories.Unchanged != 2 || again.Schedules.Unchanged != 2 || again.Relations.Unchanged != 1 || again.Writes != 0 ||
			again.Projects.Created+again.Tasks.Created+again.Notes.Created+again.Directories.Created+again.Relations.Created+again.Schedules.Created != 0 {
			t.Fatalf("second import (apply %v): %+v", apply, again)
		}
		if r2, l2, s2 := census(t, store); r2 != recs || l2 != rels || s2 != sets {
			t.Fatalf("second import (apply %v) changed the store: %d/%d/%d -> %d/%d/%d", apply, recs, rels, sets, r2, l2, s2)
		}
	}

	// The source is as it was, and a write through its handle fails.
	h, err := openSource(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{`INSERT INTO projects (id, row) VALUES ('x', '{}')`, `CREATE TABLE x (a)`, `PRAGMA journal_mode=WAL`} {
		if _, err := h.db.Exec(stmt); err == nil && !strings.HasPrefix(stmt, "PRAGMA") {
			t.Errorf("%s through the source handle succeeded", stmt)
		}
	}
	h.Close()
	if after := fileState(t, src, true); after != before {
		t.Fatalf("source changed:\n%s\nwas:\n%s", after, before)
	}

	// A changed v1 row patches its record and nothing else.
	db, err := sql.Open("sqlite", src)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE tasks SET row = json_set(row, '$.title', 'top, renamed') WHERE id = 't1'`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if upd := run(t, store, src, true); upd.Tasks.Updated != 1 || upd.Tasks.Unchanged != 6 || upd.Writes != 1 {
		t.Fatalf("update: %+v", upd.Tasks)
	}
	if got := byLegacy(t, store, data.KindTask, "task:t1"); got.Text("title") != "top, renamed" || got.ID != t1.ID {
		t.Errorf("updated t1: %+v", got)
	}
}

func TestImportMissingTables(t *testing.T) {
	src := filepath.Join(t.TempDir(), "mtm.db")
	writeV1(t, src, true, map[string][][2]string{
		"projects": {{"p1", `{"id":"p1","name":"solo","status":"open"}`}},
		"tasks":    {{"t1", `{"id":"t1","project_id":"p1","title":"one","status":"todo","surprise":{"k":"v"}}`}},
		"notes":    nil, // another shape
	})
	if exists(src+"-wal") || exists(src+"-shm") {
		t.Fatal("the fixture left -wal or -shm beside it")
	}
	before := fileState(t, src, true)
	rep := run(t, nil, src, false)
	if !rep.Snapshot || !slices.Equal(rep.MissingTables, []string{"directories", "relations", "schedule_defs", "reminder_defs"}) ||
		rep.Projects.Created != 1 || rep.Tasks.Created != 1 || !strings.Contains(strings.Join(rep.Warnings, "\n"), "table notes unreadable") {
		t.Fatalf("dry run: %+v", rep)
	}
	store := openV2(t)
	if rep := run(t, store, src, true); rep.Tasks.Created != 1 {
		t.Fatalf("apply: %+v", rep)
	}
	if got := byLegacy(t, store, data.KindTask, "task:t1"); !strings.Contains(got.Text("legacyExtra"), `"surprise"`) {
		t.Errorf("unknown key not kept: %+v", got)
	}
	if after := fileState(t, src, true); after != before {
		t.Fatalf("WAL source changed:\n%s\nwas:\n%s", after, before)
	}
}

// TestImportRealFile dry-runs the owner's v1 database when there is one, then
// applies it into a temporary store. The file is only ever opened read-only.
func TestImportRealFile(t *testing.T) {
	home, _ := os.UserHomeDir()
	src := filepath.Join(home, ".local", "share", "matomeru", "mtm.db")
	info, err := os.Stat(src)
	if err != nil {
		t.Skip("no v1 database at " + src)
	}
	before := fileState(t, src, false)
	rep := run(t, nil, src, false)
	if rep.Projects.Total == 0 || rep.Tasks.Total == 0 {
		t.Fatalf("dry run found nothing: %+v", rep)
	}
	after, err := os.Stat(src)
	if err != nil || !after.ModTime().Equal(info.ModTime()) || after.Size() != info.Size() {
		t.Fatalf("v1 file changed: %v %v -> %v %v (%v)", info.ModTime(), info.Size(), after.ModTime(), after.Size(), err)
	}
	if got := fileState(t, src, false); got != before {
		t.Fatalf("files beside the v1 database changed:\n%s\nwas:\n%s", got, before)
	}
	out, _ := json.MarshalIndent(rep, "", "  ")
	t.Logf("dry run: %s", out)

	store := openV2(t)
	applied := run(t, store, src, true)
	again := run(t, store, src, true)
	t.Logf("apply: %d ms, %d writes; again: %d ms, %d writes, tasks %+v", applied.ElapsedMS, applied.Writes, again.ElapsedMS, again.Writes, again.Tasks)
	if again.Writes != 0 {
		t.Errorf("second apply wrote %d change sets", again.Writes)
	}
}
