package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/unixsuperhero/ochazuke/refdoc"
)

func TestWriteVersionNamesVersionCommitAndOchazuke(t *testing.T) {
	var out bytes.Buffer
	s := refdoc.Stamp{Tool: "mtm", Version: "v0.1.0", Commit: "0123456789abcdef", Date: "2026-10-29", Ochazuke: "v0.51.0"}
	if err := writeVersion(&out, s); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "mtm v0.1.0 (commit 0123456789ab, 2026-10-29, ochazuke v0.51.0)\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestVersionFlagPrintsWithoutOpeningTheDatabase(t *testing.T) {
	var out, errOut bytes.Buffer
	data := t.TempDir() + "/never.db"
	if err := run([]string{"--data", data, "--version"}, strings.NewReader(""), &out, &errOut); err != nil {
		t.Fatal(err, errOut.String())
	}
	if !strings.HasPrefix(out.String(), "mtm ") || !strings.Contains(out.String(), "(commit ") {
		t.Fatalf("version line: %q", out.String())
	}
	if _, err := os.Stat(data); err == nil {
		t.Fatalf("--version created %s", data)
	}
}
