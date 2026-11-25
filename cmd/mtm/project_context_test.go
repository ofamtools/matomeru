package main

import (
	"path/filepath"
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
)

func TestProjectContextCLIWorkflow(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "mtm.db")
	c := cli{t: t, db: db}

	proj := decode[record](t, c.ok("task", "create", "--title", "Apollo", "--field", "defaultDir=/repo/apollo"))
	child := decode[record](t, c.ok("task", "create", "--title", "Implement Stage 1", "--field", "parent="+proj.ID))
	c.ok("task", "flag", child.ID)

	// 1. mtm context add-official
	added := decode[contextAddOfficialOutput](t, c.ok(
		"context", "add-official",
		"https://docs.google.com/document/d/apollo-prd/edit",
		"--project", child.ID,
		"--title", "Apollo PRD",
		"--notes", "Primary architecture spec.",
		"--json",
	))
	if added.ProjectID != proj.ID || added.Deduplicated || added.Context == nil {
		t.Fatalf("add-official = %+v", added)
	}
	if !added.Context.Bool("official") || !added.Context.Bool("pinned") || added.Context.Text("contextKind") != "gdoc" {
		t.Fatalf("add-official fields = %+v", added.Context.Fields)
	}

	// Adding the same URL again deduplicates cleanly.
	addedAgain := decode[contextAddOfficialOutput](t, c.ok(
		"context", "add-official",
		"https://docs.google.com/document/d/apollo-prd/edit/",
		"--project", proj.ID,
		"--json",
	))
	if !addedAgain.Deduplicated || addedAgain.Context.ID != added.Context.ID {
		t.Fatalf("expected deduplication, got %+v", addedAgain)
	}

	// Create a non-official context item on the project.
	extra := decode[record](t, c.ok(
		"context", "create",
		"--title", "Scratch Link",
		"--field", "value=https://example.com/scratch",
		"--field", "task="+proj.ID,
	))

	// 2. mtm context pin / unpin / official
	unpinned := decode[record](t, c.ok("context", "unpin", added.Context.ID, "--json"))
	if unpinned.Fields["pinned"] != false {
		t.Fatalf("unpin = %+v", unpinned.Fields)
	}
	pinned := decode[record](t, c.ok("context", "pin", added.Context.ID, "--json"))
	if pinned.Fields["pinned"] != true {
		t.Fatalf("pin = %+v", pinned.Fields)
	}

	promoted := decode[record](t, c.ok("context", "official", extra.ID, "--notes", "Useful reference", "--json"))
	if promoted.Fields["official"] != true || promoted.Fields["notes"] != "Useful reference" {
		t.Fatalf("official promote = %+v", promoted.Fields)
	}
	demoted := decode[record](t, c.ok("context", "official", extra.ID, "--off", "--json"))
	if demoted.Fields["official"] != false {
		t.Fatalf("official --off = %+v", demoted.Fields)
	}

	// 3. mtm context project [--all]
	projOut := decode[contextProjectOutput](t, c.ok("context", "project", "--project", proj.ID, "--all", "--json"))
	if projOut.ProjectID != proj.ID || len(projOut.Official) != 1 || len(projOut.All) != 2 || len(projOut.Worktrees) != 1 {
		t.Fatalf("context project = %+v", projOut)
	}
	humanProj := c.ok("context", "project", "--project", proj.ID)
	if !strings.Contains(humanProj, "Apollo PRD") || !strings.Contains(humanProj, "/repo/apollo") {
		t.Fatalf("human context project output:\n%s", humanProj)
	}

	// 4. mtm task queue & dispatch auto-inject, opt-out (--no-project-context), and override (--context)
	qDef := decode[taskQueueOutput](t, c.ok("task", "queue", "--project", proj.ID, "--json"))
	if qDef.Count != 1 || len(qDef.Items[0].ProjectContext) != 1 || qDef.Items[0].ProjectContext[0].ID != added.Context.ID {
		t.Fatalf("task queue default projectContext = %+v", qDef.Items)
	}

	qNoCtx := decode[taskQueueOutput](t, c.ok("task", "queue", "--project", proj.ID, "--no-project-context", "--json"))
	if qNoCtx.Count != 1 || len(qNoCtx.Items[0].ProjectContext) != 0 {
		t.Fatalf("task queue --no-project-context = %+v", qNoCtx.Items)
	}

	qOverride := decode[taskQueueOutput](t, c.ok("task", "queue", "--project", proj.ID, "--context", extra.ID+","+added.Context.ID, "--json"))
	if qOverride.Count != 1 || len(qOverride.Items[0].ProjectContext) != 2 || qOverride.Items[0].ProjectContext[0].ID != extra.ID {
		t.Fatalf("task queue --context = %+v", qOverride.Items)
	}

	dispDef := decode[taskDispatchOutput](t, c.ok("task", "dispatch", "--project", proj.ID, "--dry-run", "--json"))
	if !strings.Contains(dispDef.Prompt, "## Official Project Context") || !strings.Contains(dispDef.Prompt, "Apollo PRD") {
		t.Fatalf("dispatch default prompt:\n%s", dispDef.Prompt)
	}

	dispNoCtx := decode[taskDispatchOutput](t, c.ok("task", "dispatch", "--project", proj.ID, "--dry-run", "--no-project-context", "--json"))
	if strings.Contains(dispNoCtx.Prompt, "## Official Project Context") {
		t.Fatalf("dispatch --no-project-context prompt:\n%s", dispNoCtx.Prompt)
	}

	dispOverride := decode[taskDispatchOutput](t, c.ok("task", "dispatch", "--project", proj.ID, "--dry-run", "--context", extra.ID, "--json"))
	if !strings.Contains(dispOverride.Prompt, "Scratch Link") || strings.Contains(dispOverride.Prompt, "Apollo PRD") {
		t.Fatalf("dispatch --context prompt:\n%s", dispOverride.Prompt)
	}

	// Ensure KindContext schema enum includes gdoc and sheet via store open.
	store, err := data.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if got, err := store.OfficialProjectContext(proj.ID); err != nil || len(got) != 1 {
		t.Fatalf("store OfficialProjectContext = %+v, err=%v", got, err)
	}
}
