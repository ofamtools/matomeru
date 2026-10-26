package contextitems

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// kinds is "kind text" per item, in order.
func kinds(items []Item) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.Kind+" "+it.Text)
	}
	return out
}

func TestExtractFindsEachKindOnce(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "main.go")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	text := strings.Join([]string{
		"see https://example.com/docs?page=2 and https://example.com/docs?page=2.",
		"opened https://github.com/unixsuperhero/matomeru/pull/42",
		"and unixsuperhero/ochazuke#7 for the fix",
		"edited " + file + ":12:3 and ./main.go",
		"Switched to a new branch 'lane/context'",
		"[lane/context 1ba025f] context: declare the table",
		"commit c4cd8374fae0d6753888d8219dbb94728a77a76f",
		"window 9332c352-fd31-49a0-bb93-6b18ed187d8d, id d282380401c62009ce27ec6e4ea0c6ed, origin/main",
		"/clear is a slash command, not a path; prose.words neither; go test ./... nor ../ either",
	}, "\n")
	got := kinds(Extract(text, 0, Source{Kind: "scrollback", Cwd: dir}))
	want := []string{
		"link https://example.com/docs?page=2",
		"pr https://github.com/unixsuperhero/matomeru/pull/42",
		"pr unixsuperhero/ochazuke#7",
		"branch lane/context",
		"file " + file,
		"commit 1ba025f",
		"commit c4cd8374fae0d6753888d8219dbb94728a77a76f",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("items:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestExtractMarksWhetherAPathExists(t *testing.T) {
	dir := t.TempDir()
	items := Extract("wrote "+dir+"/ and /tmp/nowhere-"+filepath.Base(dir)+"/x.txt", 0, Source{})
	if len(items) != 2 {
		t.Fatalf("items = %v", kinds(items))
	}
	if items[0].Kind != "dir" || !*items[0].Exists {
		t.Fatalf("existing dir = %+v", items[0])
	}
	if items[1].Kind != "file" || *items[1].Exists {
		t.Fatalf("missing file = %+v", items[1])
	}
}

// A URL longer than the pane wraps onto the next screen line; joined, it is
// one item.
func TestAURLWrappedAcrossTwoScreenLinesIsOneItem(t *testing.T) {
	url := "https://github.com/unixsuperhero/matomeru/pull/1234/files"
	width := 40
	screen := "PR: " + url[:width-4] + "\n" + url[width-4:] + "\nnext line"
	items := Extract(screen, width, Source{})
	if got := kinds(items); !slices.Equal(got, []string{"pr " + url}) {
		t.Fatalf("items = %v", got)
	}
}

func TestBranchesFromGitAndPrompts(t *testing.T) {
	text := "On branch main\n[ STUDIO ] git:video-capture !?\n * [new branch]      fix/live-cursor -> fix/live-cursor\n(HEAD -> lane/uxr2, origin/main)"
	got := kinds(Extract(text, 0, Source{}))
	want := []string{"branch main", "branch fix/live-cursor", "branch lane/uxr2", "branch video-capture"}
	if !slices.Equal(got, want) {
		t.Fatalf("items = %v, want %v", got, want)
	}
}

func TestPromptAndCommandEntriesAreItems(t *testing.T) {
	items := Extract("go test ./...", 0, Source{Kind: "history", EntryKind: "command"})
	if len(items) == 0 || items[0].Kind != "command" || items[0].Text != "go test ./..." {
		t.Fatalf("items = %v", kinds(items))
	}
}

func TestTargetOpensAPRReferenceOnGitHub(t *testing.T) {
	if got := Target(Item{Kind: "pr", Text: "unixsuperhero/ochazuke#7"}); got != "https://github.com/unixsuperhero/ochazuke/issues/7" {
		t.Fatal(got)
	}
}

func TestPathsLoseLocationsAndTruncatedOnesAreDropped(t *testing.T) {
	text := "read /tmp/app/build.mjs:27:raw, /tmp/app/section.html:1-7,565 and /tmp/app/SKILL.md⟧\nnot /tmp/proj/matom… or /tmp/proj/{a,b} or /tmp/x/**/*.go\nwindow (acfec4d5) and 20261023 0f747cf1"
	got := kinds(Extract(text, 0, Source{}))
	want := []string{"file /tmp/app/build.mjs", "file /tmp/app/section.html", "file /tmp/app/SKILL.md"}
	if !slices.Equal(got, want) {
		t.Fatalf("items = %v, want %v", got, want)
	}
}
