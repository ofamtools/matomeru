package data

import (
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"
)

func makeGitRepo(t *testing.T, path string, worktree bool) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(path, ".git")
	if worktree {
		if err := os.WriteFile(marker, []byte("gitdir: /path/to/git/worktrees/example\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	} else if err := os.Mkdir(marker, 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestScanGitReposDepthAndPruning(t *testing.T) {
	base := t.TempDir()
	makeGitRepo(t, filepath.Join(base, "repo"), false)
	makeGitRepo(t, filepath.Join(base, "repo", "nested"), false)
	makeGitRepo(t, filepath.Join(base, "group", "worktree"), true)
	makeGitRepo(t, filepath.Join(base, "group", "nested", "edge"), false)
	makeGitRepo(t, filepath.Join(base, "group", "nested", "other", "beyond"), false)
	for _, skip := range []string{"node_modules", "vendor", ".worktrees", "dist", "build", "target", ".cache"} {
		makeGitRepo(t, filepath.Join(base, skip, "hidden"), false)
	}
	want := []DiscoveredRepo{
		{Path: filepath.Join(base, "group", "nested", "edge"), Name: "edge", BaseDir: base, Depth: 3},
		{Path: filepath.Join(base, "group", "worktree"), Name: "worktree", BaseDir: base, Depth: 2},
		{Path: filepath.Join(base, "repo"), Name: "repo", BaseDir: base, Depth: 1},
	}
	got, err := ScanGitRepos(base, 0)
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("default-depth scan = %+v, %v, want %+v", got, err, want)
	}
	got, err = ScanGitRepos(base, 1)
	if err != nil || !slices.Equal(got, want[2:]) {
		t.Fatalf("depth-one scan = %+v, %v", got, err)
	}
	gitRoot := filepath.Join(base, "repo")
	got, err = ScanGitRepos(gitRoot, 5)
	if err != nil || len(got) != 1 || got[0].BaseDir != gitRoot || got[0].Depth != 0 {
		t.Fatalf("repository as scan root = %+v, %v", got, err)
	}
}

func TestBaseDirStorePersistenceAndDepth(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	base := filepath.Join(home, "repos ")
	near := filepath.Join(base, "near")
	group := filepath.Join(base, "group")
	far := filepath.Join(group, "far")
	makeGitRepo(t, near, false)
	makeGitRepo(t, far, true)
	file := filepath.Join(home, "state", "bases.json")
	store := NewBaseDirStore(file)
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	got, err := store.Add("~/repos /./", 1, now)
	if err != nil || len(got) != 1 || got[0].Path != near {
		t.Fatalf("add = %+v, %v", got, err)
	}
	got[0].Path = "caller edited returned copy"
	if got := store.Repos(); len(got) != 1 || got[0].Path != near {
		t.Fatalf("stored repos = %+v", got)
	}
	store = NewBaseDirStore(file)
	bases := store.List()
	if want := []BaseDir{{Path: base, MaxDepth: 1, LastScannedAt: now, RepoCount: 1}}; !slices.Equal(bases, want) {
		t.Fatalf("restored bases = %+v, want %+v", bases, want)
	}
	if got := store.Repos(); len(got) != 1 || got[0].Path != near {
		t.Fatalf("restored repos = %+v", got)
	}
	if _, err := store.Add(group, 1, now); err != nil {
		t.Fatal(err)
	}
	later := now.Add(time.Hour)
	got, err = store.ScanAll(2, later)
	if err != nil || len(got) != 2 || got[0].Path != far || got[0].BaseDir != group || got[1].Path != near {
		t.Fatalf("rescan = %+v, %v", got, err)
	}
	if got := store.List(); len(got) != 2 || got[0].MaxDepth != 2 || got[0].RepoCount != 2 || !got[0].LastScannedAt.Equal(later) {
		t.Fatalf("rescanned bases = %+v", got)
	}
	if err := store.Remove(group); err != nil {
		t.Fatal(err)
	}
	if got := NewBaseDirStore(file).Repos(); len(got) != 2 || got[0].BaseDir != base {
		t.Fatalf("repos after removing overlapping base = %+v", got)
	}
	if err := store.Remove("~/repos "); err != nil {
		t.Fatal(err)
	}
	if reopened := NewBaseDirStore(file); len(reopened.List()) != 0 || len(reopened.Repos()) != 0 {
		t.Fatalf("state after removing final base = %+v, %+v", reopened.List(), reopened.Repos())
	}
}

func TestBaseDirStoreFailedRescanPreservesSnapshot(t *testing.T) {
	parent := t.TempDir()
	baseA, baseZ := filepath.Join(parent, "a"), filepath.Join(parent, "z")
	makeGitRepo(t, baseA, false)
	makeGitRepo(t, baseZ, false)
	store := NewBaseDirStore(filepath.Join(parent, "bases.json"))
	now := time.Now().UTC()
	for _, base := range []string{baseA, baseZ} {
		if _, err := store.Add(base, 1, now); err != nil {
			t.Fatal(err)
		}
	}
	beforeBases, beforeRepos := store.List(), store.Repos()
	if err := os.RemoveAll(baseZ); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ScanAll(2, now.Add(time.Hour)); err == nil {
		t.Fatal("rescan succeeded after losing a base")
	}
	if !slices.Equal(store.List(), beforeBases) || !slices.Equal(store.Repos(), beforeRepos) {
		t.Fatalf("failed scan changed saved snapshot to %+v, %+v", store.List(), store.Repos())
	}
}

func TestEnsureProjectTasksForRepos(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store := openTest(t)
	existing := create(t, store, KindTask, map[string]any{"title": "existing", "defaultDir": "~/existing/"})
	create(t, store, KindTask, map[string]any{"title": "child", "defaultDir": filepath.Join(home, "child"), "parent": existing.Ref()})
	archived := create(t, store, KindTask, map[string]any{"title": "archived", "defaultDir": filepath.Join(home, "archived")})
	if _, _, err := store.Archive(archived.Ref(), owner); err != nil {
		t.Fatal(err)
	}
	repos := []DiscoveredRepo{
		{Path: filepath.Join(home, "existing"), Name: "skip"},
		{Path: filepath.Join(home, "child"), Name: "import child path"},
		{Path: filepath.Join(home, "archived"), Name: "import archived path"},
		{Path: "~/child/", Name: "skip same batch"},
	}
	var calls sync.WaitGroup
	created := make(chan int, 2)
	for range cap(created) {
		calls.Go(func() {
			count, err := EnsureProjectTasksForRepos(store, repos, owner)
			if err != nil {
				t.Error(err)
			}
			created <- count
		})
	}
	calls.Wait()
	close(created)
	var counts []int
	for count := range created {
		counts = append(counts, count)
	}
	slices.Sort(counts)
	if !slices.Equal(counts, []int{0, 2}) {
		t.Fatalf("concurrent import created %v, want [0 2]", counts)
	}
	roots, err := store.Query(Query{Kind: KindTask, Roots: true})
	if err != nil || !slices.Equal(titles(roots.Items), []string{"existing", "import child path", "import archived path"}) {
		t.Fatalf("roots = %v, %v", titles(roots.Items), err)
	}
	for i, rec := range roots.Items[1:] {
		if got := rec.Text("defaultDir"); got != repos[i+1].Path {
			t.Errorf("root %q defaultDir = %q", rec.Text("title"), got)
		}
	}
	if roots.Items[1].ChangeSetNumber != roots.Items[2].ChangeSetNumber {
		t.Fatalf("imported roots use separate change sets")
	}
}
