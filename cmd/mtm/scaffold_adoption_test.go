package main

import (
	"testing"

	"ofam.dev/ochazuke"
)

func TestScaffoldEmptyListsCreateWithN(t *testing.T) {
	t.Parallel()
	for _, surface := range []string{"index", "curated"} {
		t.Run(surface, func(t *testing.T) {
			t.Parallel()
			a := open(t)
			list := a.cx.list
			if surface == "index" {
				if err := a.r.OpenIndex(contextNoun, ""); err != nil {
					t.Fatal(err)
				}
				list = a.r.List(ochazuke.IndexPageID(contextNoun) + "/list")
			} else if err := a.r.OpenPage(contextPage); err != nil {
				t.Fatal(err)
			}
			if err := list.Focus(); err != nil {
				t.Fatal(err)
			}
			if list.Len() != 0 {
				t.Fatalf("expected an empty %s list, got %d rows", surface, list.Len())
			}
			press(t, a, "n")
			if a.r.RecordEditor(ochazuke.ArgsFormID) == nil || a.r.FocusID() != ochazuke.ArgsFormID+"/text" || list.Len() != 0 {
				t.Fatalf("n must ask before creating: %s\n%s", where(a), frame(a))
			}
			typeText(t, a, "first context on "+surface)
			pressSettle(t, a, "enter")
			items, err := a.contexts.All()
			if err != nil || len(items) != 1 || items[0].Text != "first context on "+surface || a.r.TopModal() != "" || list.Len() != 1 {
				t.Fatalf("n did not persist and refill %s: %+v, %v\n%s", surface, items, err, frame(a))
			}
		})
	}
}

func TestScaffoldRelationCreateOwnFromAndCopy(t *testing.T) {
	t.Parallel()
	for _, route := range []string{"CLI", "palette"} {
		t.Run(route, func(t *testing.T) {
			t.Parallel()
			a := open(t)
			run := func(words ...string) {
				t.Helper()
				if route == "CLI" {
					mustCLI(t, a, append([]string{"relation", "create"}, words...)...)
					return
				}
				line := "relation create"
				for _, word := range words {
					line += " " + word
				}
				press(t, a, ":")
				typeText(t, a, line)
				pressSettle(t, a, "enter")
				if a.r.TopModal() != "" {
					t.Fatalf("create asked instead of writing: %s\n%s", where(a), frame(a))
				}
			}
			run("kind=adoption", "from=project:matomeru", "to=project:ochazuke", "text=retained", "position=2048")
			source := relationsOf(t, a, ochazuke.RelationQuery{Kind: "adoption", From: "project:matomeru", To: "project:ochazuke"})
			if len(source) != 1 || source[0].Text != "retained" || source[0].Position != 2048 {
				t.Fatalf("own from property was not created: %+v", source)
			}
			stored, err := a.relStore.Rows(ochazuke.RelationQuery{Kind: "adoption"})
			if err != nil || len(stored) != 1 {
				t.Fatalf("source relation: %+v %v", stored, err)
			}
			run("create-from="+stored[0].Key, "to=task:t1")
			copied := relationsOf(t, a, ochazuke.RelationQuery{Kind: "adoption", From: "project:matomeru", To: "task:t1"})
			if len(copied) != 1 || copied[0].Text != "retained" || copied[0].Position != 2048 {
				t.Fatalf("create-from did not retain copied props: %+v", copied)
			}
			original, err := a.relationRow(stored[0].Key)
			if err != nil || original.To != "project:ochazuke" || original.Text != "retained" {
				t.Fatalf("copy changed the original: %+v %v", original, err)
			}
		})
	}
}
