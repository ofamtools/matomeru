package main

import (
	"path/filepath"
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
)

func TestClipPutListCopyCLI(t *testing.T) {
	t.Setenv("MTM_PANE_ID", "")
	t.Setenv("MTM_TASK_ID", "")
	var copied []string
	orig := systemClipboardCopy
	systemClipboardCopy = func(text string) error { copied = append(copied, text); return nil }
	t.Cleanup(func() { systemClipboardCopy = orig })

	c := cli{t: t, db: filepath.Join(t.TempDir(), "mtm.db")}
	first := decode[record](t, c.ok("clip", "put", "git", "status", "--kind", "code", "--project", "P1", "--copy"))
	if first.Kind != data.KindClip || first.Fields["content"] != "git status" || first.Fields["clipKind"] != "code" || first.Fields["authorKind"] != "owner" {
		t.Fatalf("clip put = %+v", first)
	}
	if len(copied) != 1 || copied[0] != "git status" {
		t.Fatalf("--copy copied %q", copied)
	}

	// Inside a pane the CLI shares as an agent; - reads stdin.
	t.Setenv("MTM_PANE_ID", "w1")
	piped := cli{t: t, db: c.db, stdin: "line one\nline two\n"}
	second := decode[record](t, piped.ok("clip", "put", "-", "--title", "Notes"))
	if second.Fields["content"] != "line one\nline two" || second.Fields["title"] != "Notes" || second.Fields["authorKind"] != "agent" {
		t.Fatalf("clip put - = %+v", second)
	}

	list := decode[[]record](t, c.ok("clip", "list"))
	if len(list) != 2 || list[0].ID != second.ID || list[1].ID != first.ID {
		t.Fatalf("clip list = %+v", list)
	}
	if only := decode[[]record](t, c.ok("clip", "list", "--project", "P1", "--limit", "5")); len(only) != 1 || only[0].ID != first.ID {
		t.Fatalf("clip list --project = %+v", only)
	}
	if table := c.ok("clip", "list", "--format", "table"); !strings.Contains(table, "Notes") || !strings.Contains(table, "CLIPKIND") {
		t.Fatalf("clip list table = %q", table)
	}

	if out := c.ok("clip", "copy", first.ID); !strings.Contains(out, "copied clip "+first.ID) || copied[len(copied)-1] != "git status" {
		t.Fatalf("clip copy = %q, copied %q", out, copied)
	}
	// No id copies the newest clip.
	if c.ok("clip", "copy"); copied[len(copied)-1] != "line one\nline two" {
		t.Fatalf("clip copy (newest) copied %q", copied)
	}
	shown := decode[record](t, c.ok("clip", "get", first.ID))
	if shown.Fields["copiedAt"] == nil || shown.Fields["copiedAt"] == "" {
		t.Fatalf("copiedAt not stamped: %+v", shown.Fields)
	}
	c.fails("clip", "put")
}
