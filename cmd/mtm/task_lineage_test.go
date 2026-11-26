package main

import (
	"path/filepath"
	"testing"

	"ofam.dev/matomeru/data"
)

func TestTaskLineageCLI(t *testing.T) {
	c := cli{t: t, db: filepath.Join(t.TempDir(), "mtm.db")}
	source := decode[data.Record](t, c.ok("task", "create", "--title", "Original", "--body", "Keep this"))
	rewrite := decode[data.Record](t, c.ok("task", "rewrite", source.ID, "--title", "Rewrite", "--body", "New body"))
	if rewrite.Text(data.FieldTaskType) != data.TaskTypeRewrite || rewrite.Text("description") != "New body" {
		t.Fatalf("rewrite result: %+v", rewrite)
	}
	parts := decode[[]data.Record](t, c.ok("task", "split", source.ID, "--parts", `[{"title":"Part one","body":"This is part one"},{"title":"Part two"}]`))
	if len(parts) != 2 || parts[0].Text("description") != "This is part one" {
		t.Fatalf("split results: %+v", parts)
	}
	joined := decode[data.Record](t, c.ok("task", "join", parts[0].ID, parts[1].ID, "--title", "Joined"))
	if joined.Text(data.FieldTaskType) != data.TaskTypeJoin {
		t.Fatalf("join result: %+v", joined)
	}
	if original := decode[data.Record](t, c.ok("task", "get", source.ID)); original.Text("description") != "Keep this" {
		t.Fatalf("source was changed: %+v", original)
	}
}

func TestConceptCLI(t *testing.T) {
	c := cli{t: t, db: filepath.Join(t.TempDir(), "mtm.db")}
	concept := decode[data.Record](t, c.ok("concept", "create", "--title", "Workflow idea", "--field", "conceptKind=workflow", "--body", "## Proposal"))
	if concept.Kind != data.KindConcept || concept.Text(data.FieldConceptKind) != data.ConceptKindWorkflow ||
		concept.Text(data.FieldConceptStatus) != data.ConceptStatusIdea || concept.Text("body") != "## Proposal" {
		t.Fatalf("concept CLI result: %+v", concept)
	}
}
