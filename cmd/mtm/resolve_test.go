package main

import (
	"io"
	"path/filepath"
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
)

func TestCLIHumanFriendlyResolutionAndPicker(t *testing.T) {
	origTTY := isInteractiveTTY
	origPicker := runCLIRecordPicker
	t.Cleanup(func() {
		isInteractiveTTY = origTTY
		runCLIRecordPicker = origPicker
	})

	dir := t.TempDir()
	db := filepath.Join(dir, "mtm.db")
	c := cli{t: t, db: db}

	t1 := decode[record](t, c.ok("task", "create", "--title", "Ship Alpha Release", "--field", "priority=P1"))
	t2 := decode[record](t, c.ok("task", "create", "--title", "Ship Beta Release", "--field", "priority=P2"))
	note := decode[record](t, c.ok("note", "create", "--title", "Release Checklist"))

	// Force non-TTY for cases 1-4 and non-TTY error checks.
	isInteractiveTTY = func(io.Reader) bool { return false }

	// 1. Exact ID.
	if got := decode[record](t, c.ok("task", "get", t1.ID)); got.ID != t1.ID {
		t.Fatalf("exact ID get = %+v, want %s", got, t1.ID)
	}
	if got := decode[record](t, c.ok("record", "get", "task", t1.ID)); got.ID != t1.ID {
		t.Fatalf("record get exact ID = %+v, want %s", got, t1.ID)
	}

	// 2. Unique case-insensitive ID prefix.
	prefixLen := 4
	for prefixLen < len(t1.ID) && strings.HasPrefix(strings.ToLower(t2.ID), strings.ToLower(t1.ID[:prefixLen])) {
		prefixLen++
	}
	prefix := strings.ToUpper(t1.ID[:prefixLen])
	if got := decode[record](t, c.ok("task", "get", prefix)); got.ID != t1.ID {
		t.Fatalf("prefix %q get = %+v, want %s", prefix, got, t1.ID)
	}
	if got := decode[record](t, c.ok("record", "get", "task", prefix)); got.ID != t1.ID {
		t.Fatalf("record get prefix %q = %+v, want %s", prefix, got, t1.ID)
	}

	// 3. Case-insensitive exact match on title.
	if got := decode[record](t, c.ok("task", "get", "ship alpha release")); got.ID != t1.ID {
		t.Fatalf("exact title get = %+v, want %s", got, t1.ID)
	}

	// 4. Unique case-insensitive substring match on title.
	starred := decode[record](t, c.ok("task", "star", "beta"))
	if starred.ID != t2.ID || starred.StarStatus != "starred" {
		t.Fatalf("substring star = %+v, want %s starred", starred, t2.ID)
	}

	// Human-friendly resolution on relate / unrelate target (<kind:title> and <kind:prefix>).
	c.ok("note", "relate", "checklist", "about", "task:alpha")
	c.ok("note", "unrelate", note.ID[:4], "about", "task:"+prefix)

	// 5a. Ambiguous match on non-TTY -> clear error listing matching candidates (id  title).
	errOut := c.fails("task", "get", "release")
	if !strings.Contains(errOut, "ambiguous") || !strings.Contains(errOut, t1.ID) || !strings.Contains(errOut, t2.ID) || !strings.Contains(errOut, "Ship Alpha Release") {
		t.Fatalf("ambiguous non-TTY error = %q", errOut)
	}

	// 6a. Omitted <id> on non-TTY -> usage error immediately without hanging.
	usageErr := c.fails("task", "star")
	if !strings.Contains(usageErr, "usage:") {
		t.Fatalf("omitted ID non-TTY error = %q", usageErr)
	}
	editUsageErr := c.fails("task", "edit", "--title", "nope")
	if !strings.Contains(editUsageErr, "usage:") {
		t.Fatalf("omitted ID edit non-TTY error = %q", editUsageErr)
	}

	// 5b. Ambiguous match on interactive TTY -> launches picker over matching candidates.
	isInteractiveTTY = func(io.Reader) bool { return true }
	var pickedCount int
	var lastTitle string
	runCLIRecordPicker = func(_ *data.Store, _ string, candidates []data.Record, title string) (string, error) {
		pickedCount = len(candidates)
		lastTitle = title
		return t2.ID, nil
	}
	gotAmb := decode[record](t, c.ok("task", "get", "release"))
	if gotAmb.ID != t2.ID || pickedCount != 2 || !strings.Contains(lastTitle, "release") {
		t.Fatalf("ambiguous TTY pick: got=%s count=%d title=%q", gotAmb.ID, pickedCount, lastTitle)
	}

	// 6b. Omitted <id> on interactive TTY -> launches picker over all active records of kind.
	c.ok("task", "archive", t2.ID)
	runCLIRecordPicker = func(_ *data.Store, _ string, candidates []data.Record, title string) (string, error) {
		pickedCount = len(candidates)
		lastTitle = title
		return candidates[0].ID, nil
	}
	edited := decode[record](t, c.ok("task", "edit", "--title", "Ship Alpha Renamed"))
	if edited.ID != t1.ID || edited.Fields["title"] != "Ship Alpha Renamed" || pickedCount != 1 {
		t.Fatalf("omitted ID TTY edit: got=%+v count=%d", edited, pickedCount)
	}
}
