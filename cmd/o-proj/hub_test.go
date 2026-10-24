package main

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unixsuperhero/matomeru/internal/store"
	"github.com/unixsuperhero/matomeru/internal/work"
)

func readlink(t *testing.T, path string) string {
	t.Helper()
	target, err := os.Readlink(path)
	if err != nil {
		t.Fatal(err)
	}
	return target
}

func TestNewLinksFromHub(t *testing.T) {
	tl, out, _ := tool(t)
	root := filepath.Join(tl.Home, "proj")
	hub := filepath.Join(t.TempDir(), "hub", "o") // created by new
	if err := tl.Main([]string{"new", "demo", "--root", root, "--hub", hub, "--no-register"}); err != nil {
		t.Fatal(err, out)
	}
	dir := filepath.Join(root, "demo")
	if got := readlink(t, filepath.Join(hub, "demo")); got != dir {
		t.Fatalf("link -> %q, want %q", got, dir)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if got := lines[len(lines)-2]; got != "link: "+filepath.Join(hub, "demo")+" -> "+dir {
		t.Fatalf("link line %q", got)
	}
	if !strings.HasPrefix(lines[len(lines)-3], "register: ") {
		t.Fatalf("register before link: %q", lines)
	}
}

func TestNewDefaultHubIsHomeHubO(t *testing.T) {
	tl, out, _ := tool(t)
	if err := tl.Main([]string{"new", "x", "--root", filepath.Join(tl.Home, "proj"), "--no-register"}); err != nil {
		t.Fatal(err, out)
	}
	if got := readlink(t, filepath.Join(tl.Home, "hub", "o", "x")); got != filepath.Join(tl.Home, "proj", "x") {
		t.Fatalf("link -> %q", got)
	}
	if !strings.Contains(out.String(), "link: ~/hub/o/x -> "+filepath.Join(tl.Home, "proj", "x")) {
		t.Fatalf("out %q", out)
	}
	tl.Env = append(tl.Env, "O_PROJ_HUB="+filepath.Join(tl.Home, "elsewhere"))
	if err := tl.Main([]string{"new", "y", "--root", filepath.Join(tl.Home, "proj"), "--no-register"}); err != nil {
		t.Fatal(err, out)
	}
	if _, err := os.Lstat(filepath.Join(tl.Home, "elsewhere", "y")); err != nil {
		t.Fatal(err)
	}
}

func TestNewHubNoneSkips(t *testing.T) {
	tl, out, _ := tool(t)
	if err := tl.Main([]string{"new", "x", "--root", filepath.Join(tl.Home, "proj"), "--hub", "none", "--no-register"}); err != nil {
		t.Fatal(err, out)
	}
	if _, err := os.Lstat(filepath.Join(tl.Home, "hub")); !os.IsNotExist(err) {
		t.Fatalf("hub made: %v", err)
	}
	if !strings.Contains(out.String(), "link: skipped (--hub none)") {
		t.Fatalf("out %q", out)
	}
}

func TestNewKeepsForeignLink(t *testing.T) {
	tl, out, _ := tool(t)
	hub := filepath.Join(tl.Home, "hub", "o")
	if err := os.MkdirAll(hub, 0o755); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(tl.Home, "other")
	if err := os.Symlink(other, filepath.Join(hub, "demo")); err != nil {
		t.Fatal(err)
	}
	if err := tl.Main([]string{"new", "demo", "--root", filepath.Join(tl.Home, "proj"), "--no-register"}); err != nil {
		t.Fatal(err, out)
	}
	if got := readlink(t, filepath.Join(hub, "demo")); got != other {
		t.Fatalf("overwrote: -> %q", got)
	}
	if !strings.Contains(out.String(), "keep: ~/hub/o/demo exists -> "+other) {
		t.Fatalf("out %q", out)
	}
	if got := lastLine(out.String()); !strings.HasPrefix(got, "cd ") {
		t.Fatalf("did not continue: %q", got)
	}
}

// twoProjects registers kept (on disk) and gone (its tree removed).
func twoProjects(t *testing.T, tl *Tool) (data, keptDir, goneDir string) {
	t.Helper()
	data = filepath.Join(tl.Home, "mtm.db")
	root := filepath.Join(tl.Home, "proj")
	for _, name := range []string{"kept", "gone"} {
		if err := tl.Main([]string{"-data", data, "new", name, "--root", root, "--hub", "none"}); err != nil {
			t.Fatal(err)
		}
	}
	goneDir = filepath.Join(root, "gone")
	if err := os.RemoveAll(goneDir); err != nil {
		t.Fatal(err)
	}
	return data, filepath.Join(root, "kept"), goneDir
}

func TestLinkAllBackfills(t *testing.T) {
	tl, out, _ := tool(t)
	data, keptDir, goneDir := twoProjects(t, tl)
	hub := filepath.Join(t.TempDir(), "o")
	out.Reset()
	if err := tl.Main([]string{"-data", data, "link", "--all", "--hub", hub}); err != nil {
		t.Fatal(err, out)
	}
	if got := readlink(t, filepath.Join(hub, "kept")); got != keptDir {
		t.Fatalf("link -> %q", got)
	}
	if _, err := os.Lstat(filepath.Join(hub, "gone")); !os.IsNotExist(err) {
		t.Fatalf("linked a missing tree: %v", err)
	}
	for _, line := range []string{"link: " + filepath.Join(hub, "kept") + " -> " + keptDir, "missing: gone tree " + goneDir + " does not exist", "linked 1, kept 0, missing 1"} {
		if !strings.Contains(out.String(), line) {
			t.Fatalf("missing %q in %q", line, out)
		}
	}
	// again: the link exists and still counts as linked; a foreign one is kept
	if err := os.Symlink("/nowhere", filepath.Join(hub, "gone")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(goneDir, 0o755); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := tl.Main([]string{"-data", data, "link", "--all", "--hub", hub}); err != nil {
		t.Fatal(err, out)
	}
	if got := lastLine(out.String()); got != "linked 1, kept 1, missing 0, skipped 0 (not made by o-proj)" {
		t.Fatalf("summary %q in %q", got, out)
	}
}

func TestLinkOneAndUsage(t *testing.T) {
	tl, out, _ := tool(t)
	data, keptDir, _ := twoProjects(t, tl)
	hub := filepath.Join(t.TempDir(), "o")
	if err := tl.Main([]string{"-data", data, "link", "kept", "--hub", hub}); err != nil {
		t.Fatal(err, out)
	}
	if got := readlink(t, filepath.Join(hub, "kept")); got != keptDir {
		t.Fatalf("link -> %q", got)
	}
	if _, err := os.Lstat(filepath.Join(hub, "gone")); !os.IsNotExist(err) {
		t.Fatal("linked more than NAME")
	}
	for _, args := range [][]string{{"link"}, {"link", "kept", "--all"}, {"link", "nosuch"}, {"link", "--all", "--hub", "none"}} {
		if err := tl.Main(append([]string{"-data", data}, args...)); err == nil {
			t.Fatalf("%q: no error", args)
		}
	}
}

func TestProjectShellLink(t *testing.T) {
	tl, out, _ := tool(t)
	data, keptDir, _ := twoProjects(t, tl)
	hub := filepath.Join(t.TempDir(), "o")
	out.Reset()
	if err := tl.Main([]string{"-data", data, "project", "link", "--all", "--hub", hub}); err != nil {
		t.Fatal(err, out)
	}
	if got := readlink(t, filepath.Join(hub, "kept")); got != keptDir {
		t.Fatalf("link -> %q", got)
	}
	s := out.String()
	var n LinkCounts
	if err := json.Unmarshal([]byte(s[strings.Index(s, "{"):]), &n); err != nil || n != (LinkCounts{Linked: 1, Missing: 1}) {
		t.Fatalf("result %q %v", s, err)
	}

	// project new takes hub like new does
	out.Reset()
	if err := tl.Main([]string{"-data", data, "project", "new", "shelled", "--root", filepath.Join(tl.Home, "proj"), "--hub", hub}); err != nil {
		t.Fatal(err, out)
	}
	if got := readlink(t, filepath.Join(hub, "shelled")); got != filepath.Join(tl.Home, "proj", "shelled") {
		t.Fatalf("link -> %q", got)
	}
	db, err := store.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	w, err := work.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if ps, err := w.Projects(); err != nil || len(ps) != 3 {
		t.Fatalf("projects %d %v", len(ps), err)
	}
}

// seedForeign registers a project the way mtm (or matomeru-old) did: no
// origin, its tree a directory on disk.
func seedForeign(t *testing.T, data, name, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	w, closeDB := openWork(t, data)
	defer closeDB()
	p := work.Project{ID: work.NewID(), Name: name, Status: "open", Created: work.Now(), Trees: []work.Tree{{Kind: "dir", Path: dir}}, DefaultTree: dir}
	if err := w.PutProject(p); err != nil {
		t.Fatal(err)
	}
}

func openWork(t *testing.T, data string) (*work.Store, func()) {
	t.Helper()
	db, err := store.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	w, err := work.Open(db)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	return w, func() { db.Close() }
}

func origins(t *testing.T, data string) map[string]string {
	t.Helper()
	w, closeDB := openWork(t, data)
	defer closeDB()
	ps, err := w.Projects()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, p := range ps {
		out[p.Name] = p.Origin
	}
	return out
}

func TestOriginSetByNewAndExisting(t *testing.T) {
	tl, out, _ := tool(t)
	data := filepath.Join(tl.Home, "mtm.db")
	root := filepath.Join(tl.Home, "proj")
	if err := tl.Main([]string{"-data", data, "new", "made", "--root", root, "--hub", "none"}); err != nil {
		t.Fatal(err, out)
	}
	// an existing repository with a commit, already registered without origin
	old := filepath.Join(root, "old")
	seedForeign(t, data, "old", old)
	git(t, tl, old, "init", "--quiet")
	if err := os.WriteFile(filepath.Join(old, "x"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, tl, old, "add", "x")
	git(t, tl, old, "commit", "--quiet", "-m", "first")
	head := git(t, tl, old, "rev-parse", "HEAD")
	seedForeign(t, data, "other", filepath.Join(root, "other"))
	if err := tl.Main([]string{"-data", data, "new", "old", "--root", root, "--existing", "--hub", "none"}); err != nil {
		t.Fatal(err, out)
	}
	if got := git(t, tl, old, "rev-parse", "HEAD"); got != head {
		t.Fatalf("--existing committed: HEAD %s, was %s", got, head)
	}
	want := map[string]string{"made": "o-proj", "old": "o-proj", "other": ""}
	if got := origins(t, data); !maps.Equal(got, want) {
		t.Fatalf("origins %v, want %v", got, want)
	}
	// adopt marks a registered project without touching its tree
	if err := tl.Main([]string{"-data", data, "adopt", "other"}); err != nil {
		t.Fatal(err, out)
	}
	if got := origins(t, data)["other"]; got != "o-proj" {
		t.Fatalf("adopt origin %q", got)
	}
	if entries, _ := os.ReadDir(filepath.Join(root, "other")); len(entries) != 0 {
		t.Fatalf("adopt wrote %v", entries)
	}
	if err := tl.Main([]string{"-data", data, "adopt", "nosuch"}); err == nil {
		t.Fatal("adopt nosuch: no error")
	}
}

func TestLinkAllSkipsForeignLinkNameDoesNot(t *testing.T) {
	tl, out, _ := tool(t)
	data, keptDir, _ := twoProjects(t, tl)
	foreignDir := filepath.Join(tl.Home, "proj", "foreign")
	seedForeign(t, data, "foreign", foreignDir)
	hub := filepath.Join(t.TempDir(), "o")
	out.Reset()
	if err := tl.Main([]string{"-data", data, "link", "--all", "--hub", hub}); err != nil {
		t.Fatal(err, out)
	}
	if got := readlink(t, filepath.Join(hub, "kept")); got != keptDir {
		t.Fatalf("link -> %q", got)
	}
	if _, err := os.Lstat(filepath.Join(hub, "foreign")); !os.IsNotExist(err) {
		t.Fatalf("--all linked a project o-proj did not make: %v", err)
	}
	if got := lastLine(out.String()); got != "linked 1, kept 0, missing 1, skipped 1 (not made by o-proj)" {
		t.Fatalf("summary %q", got)
	}
	if err := tl.Main([]string{"-data", data, "link", "foreign", "--hub", hub}); err != nil {
		t.Fatal(err, out)
	}
	if got := readlink(t, filepath.Join(hub, "foreign")); got != foreignDir {
		t.Fatalf("link NAME -> %q", got)
	}
}

func TestUnlinkAllRemovesOnlyForeignLinks(t *testing.T) {
	tl, out, _ := tool(t)
	data, keptDir, _ := twoProjects(t, tl)
	root := filepath.Join(tl.Home, "proj")
	for _, name := range []string{"foreign", "moved", "plain"} {
		seedForeign(t, data, name, filepath.Join(root, name))
	}
	hub := filepath.Join(tl.Home, "hub", "o")
	if err := os.MkdirAll(hub, 0o755); err != nil {
		t.Fatal(err)
	}
	links := map[string]string{
		"kept":     keptDir,                             // o-proj made it: stays
		"foreign":  filepath.Join(root, "foreign"),      // removed
		"moved":    filepath.Join(tl.Home, "elsewhere"), // not moved's tree: kept
		"ai-utils": filepath.Join(tl.Home, "ai"),        // no registered project: untouched
	}
	for name, target := range links {
		if err := os.Symlink(target, filepath.Join(hub, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(hub, "plain"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := tl.Main([]string{"-data", data, "unlink", "--all"}); err != nil {
		t.Fatal(err, out)
	}
	if _, err := os.Lstat(filepath.Join(hub, "foreign")); !os.IsNotExist(err) {
		t.Fatalf("foreign link not removed: %v", err)
	}
	for _, name := range []string{"kept", "moved", "ai-utils"} {
		if got := readlink(t, filepath.Join(hub, name)); got != links[name] {
			t.Fatalf("%s -> %q", name, got)
		}
	}
	if _, err := os.Lstat(filepath.Join(hub, "plain")); err != nil {
		t.Fatalf("plain file touched: %v", err)
	}
	for _, line := range []string{
		"unlink: ~/hub/o/foreign -> " + links["foreign"],
		"keep: ~/hub/o/moved -> " + links["moved"] + " (not moved's tree)",
		"keep: ~/hub/o/plain exists (not a link)",
		"unlinked 1, kept 2",
	} {
		if !strings.Contains(out.String(), line) {
			t.Fatalf("missing %q in %q", line, out)
		}
	}
	// NAME unlinks that project whatever made it
	out.Reset()
	if err := tl.Main([]string{"-data", data, "unlink", "kept"}); err != nil {
		t.Fatal(err, out)
	}
	if _, err := os.Lstat(filepath.Join(hub, "kept")); !os.IsNotExist(err) {
		t.Fatalf("unlink NAME left it: %v", err)
	}
	for _, args := range [][]string{{"unlink"}, {"unlink", "kept", "--all"}, {"unlink", "nosuch"}, {"unlink", "--all", "--hub", "none"}} {
		if err := tl.Main(append([]string{"-data", data}, args...)); err == nil {
			t.Fatalf("%q: no error", args)
		}
	}
}
