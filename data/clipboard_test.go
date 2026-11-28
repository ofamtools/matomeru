package data

import (
	"testing"
	"time"

	"ofam.dev/ochazuke/core"
)

func clipStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestClipPutDerivesTitleKindAndRootProject(t *testing.T) {
	store := clipStore(t)
	actor := core.Actor{Kind: "agent", ID: "test"}
	proj, _, err := store.Create(CreateInput{Kind: KindTask, Fields: map[string]any{"title": "Proj"}}, actor)
	if err != nil {
		t.Fatal(err)
	}
	child, _, err := store.Create(CreateInput{Kind: KindTask, Fields: map[string]any{"title": "Child", "parent": proj.Ref()}}, actor)
	if err != nil {
		t.Fatal(err)
	}

	rec, err := store.PutClip(ClipInput{Content: "https://example.com/pr/1", AuthorKind: ClipAuthorAgent, FromPane: "p1", Project: child.ID}, actor)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Text("title") != "https://example.com/pr/1" || rec.Text("clipKind") != ClipKindURL || rec.Text("authorKind") != ClipAuthorAgent {
		t.Fatalf("clip = %+v", rec.Fields)
	}
	if ref, _ := rec.RefField("project"); ref.ID != proj.ID {
		t.Fatalf("project = %+v, want root %s", ref, proj.ID)
	}
	if ref, _ := rec.RefField("fromPane"); ref.Kind != KindPane || ref.ID != "p1" {
		t.Fatalf("fromPane = %+v", ref)
	}

	// Defaults: owner text; an unknown project is kept, never refused.
	plain, err := store.PutClip(ClipInput{Content: "first line\nsecond", Project: "nope"}, actor)
	if err != nil {
		t.Fatal(err)
	}
	if plain.Text("title") != "first line" || plain.Text("clipKind") != ClipKindText || plain.Text("authorKind") != ClipAuthorOwner {
		t.Fatalf("plain = %+v", plain.Fields)
	}
	if path, _ := store.PutClip(ClipInput{Content: "~/proj/x.go"}, actor); path.Text("clipKind") != ClipKindPath {
		t.Fatalf("path kind = %q", path.Text("clipKind"))
	}

	for _, bad := range []ClipInput{{Content: "  "}, {Content: "x", Kind: "blob"}, {Content: "x", AuthorKind: "system"}} {
		if _, err := store.PutClip(bad, actor); err == nil {
			t.Fatalf("PutClip(%+v) = nil error", bad)
		}
	}
}

func TestClipListNewestFirstFiltersAndCopiedAt(t *testing.T) {
	store := clipStore(t)
	actor := core.Actor{Kind: "user", ID: "owner"}
	put := func(in ClipInput) *Record {
		t.Helper()
		rec, err := store.PutClip(in, actor)
		if err != nil {
			t.Fatal(err)
		}
		return rec
	}
	a := put(ClipInput{Content: "one", Project: "P1"})
	b := put(ClipInput{Content: "two", AuthorKind: ClipAuthorAgent, Kind: ClipKindCode, Project: "P2"})
	c := put(ClipInput{Content: "three", AuthorKind: ClipAuthorAgent, Project: "task:P1"})

	ids := func(f ClipFilter) []string {
		t.Helper()
		recs, err := store.ListClips(f)
		if err != nil {
			t.Fatal(err)
		}
		out := make([]string, len(recs))
		for i, r := range recs {
			out[i] = r.ID
		}
		return out
	}
	eq := func(name string, got []string, want ...string) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("%s = %v, want %v", name, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%s = %v, want %v", name, got, want)
			}
		}
	}
	eq("all", ids(ClipFilter{}), c.ID, b.ID, a.ID)
	eq("limit", ids(ClipFilter{Limit: 2}), c.ID, b.ID)
	eq("project", ids(ClipFilter{Project: "P1"}), c.ID, a.ID)
	eq("author", ids(ClipFilter{AuthorKind: ClipAuthorAgent}), c.ID, b.ID)
	eq("kind", ids(ClipFilter{ClipKind: ClipKindCode}), b.ID)

	at := time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)
	got, err := store.MarkClipCopied("clip:"+a.ID, at, actor)
	if err != nil {
		t.Fatal(err)
	}
	if got.Text("copiedAt") != at.Format(TimeFormat) {
		t.Fatalf("copiedAt = %q", got.Text("copiedAt"))
	}
	if _, err := store.GetClip("missing"); err == nil {
		t.Fatal("GetClip(missing) = nil error")
	}
	// An archived clip leaves the shared clipboard.
	if _, _, err := store.Archive(b.Ref(), actor); err != nil {
		t.Fatal(err)
	}
	eq("after archive", ids(ClipFilter{}), c.ID, a.ID)
}
