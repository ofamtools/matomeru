package data

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"ofam.dev/ochazuke/core"
)

func makeFakeGitRepo(t *testing.T, dir, branch, remoteURL string) string {
	t.Helper()
	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if branch != "" {
		if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/"+branch+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if remoteURL != "" {
		cfg := "[core]\n\trepositoryformatversion = 0\n[remote \"origin\"]\n\turl = " + remoteURL + "\n"
		if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte(cfg), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestUpsertRepoDedupeByPathAndName(t *testing.T) {
	s := openTest(t)
	actor := core.Actor{Kind: "user", ID: "owner"}

	repoPath := filepath.Join(t.TempDir(), "matomeru")
	first, created, err := UpsertRepo(s, RepoInput{
		Name:   "matomeru",
		Path:   repoPath,
		Source: "manual",
	}, actor)
	if err != nil || !created {
		t.Fatalf("first upsert = %+v created=%v err=%v", first, created, err)
	}

	// Same path fills missing metadata without overwriting manual values or creating a duplicate.
	second, created, err := UpsertRepo(s, RepoInput{
		Name:          "matomeru-other",
		Path:          repoPath,
		RemoteURL:     "git@github.com:ofam/matomeru.git",
		DefaultBranch: "main",
		Source:        "basedir",
	}, actor)
	if err != nil || created {
		t.Fatalf("second upsert = %+v created=%v err=%v", second, created, err)
	}
	if second.ID != first.ID {
		t.Fatalf("expected same record ID %s, got %s", first.ID, second.ID)
	}
	if second.Text("name") != "matomeru" || second.Text("source") != "manual" ||
		second.Text("remoteUrl") != "git@github.com:ofam/matomeru.git" || second.Text("defaultBranch") != "main" {
		t.Fatalf("second fields = %+v", second.Fields)
	}

	// Case-insensitive name dedupe when path is omitted.
	third, created, err := UpsertRepo(s, RepoInput{
		Name:   "Matomeru",
		Source: "harvested",
	}, actor)
	if err != nil || created || third.ID != first.ID {
		t.Fatalf("third upsert = %+v created=%v err=%v", third, created, err)
	}

	res, err := s.Query(Query{Kind: KindRepo})
	if err != nil || len(res.Items) != 1 {
		t.Fatalf("expected 1 repo record, got %d (err=%v)", len(res.Items), err)
	}
}

func TestDetectGitRepoRootNormalAndWorktree(t *testing.T) {
	base := t.TempDir()
	normal := makeFakeGitRepo(t, filepath.Join(base, "alpha"), "main", "git@github.com:org/alpha.git")
	nested := filepath.Join(normal, "pkg", "sub")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	det, ok := DetectGitRepoRoot(nested)
	if !ok {
		t.Fatal("expected DetectGitRepoRoot to find normal git repo")
	}
	if det.Name != "alpha" || det.Path != normal || det.DefaultBranch != "main" || det.RemoteURL != "git@github.com:org/alpha.git" {
		t.Fatalf("normal detect = %+v", det)
	}

	// Simulate a git worktree: .git file pointing into main repo's .git/worktrees/wt1 with commondir ../..
	wtGitDir := filepath.Join(normal, ".git", "worktrees", "wt1")
	if err := os.MkdirAll(wtGitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wtGitDir, "HEAD"), []byte("ref: refs/heads/feat/wt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wtGitDir, "commondir"), []byte("../..\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wtRoot := filepath.Join(base, "alpha-wt")
	wtSub := filepath.Join(wtRoot, "cmd")
	if err := os.MkdirAll(wtSub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wtRoot, ".git"), []byte("gitdir: "+wtGitDir+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	wtDet, ok := DetectGitRepoRoot(wtSub)
	if !ok {
		t.Fatal("expected DetectGitRepoRoot to find worktree git repo")
	}
	if wtDet.Name != "alpha-wt" || wtDet.Path != wtRoot || wtDet.DefaultBranch != "feat/wt" || wtDet.RemoteURL != "git@github.com:org/alpha.git" {
		t.Fatalf("worktree detect = %+v", wtDet)
	}

	// Non-repo directory returns false.
	if _, ok := DetectGitRepoRoot(t.TempDir()); ok {
		t.Fatal("expected non-git temp dir to return ok=false")
	}
}

func TestSyncReposAndRecordRepos(t *testing.T) {
	s := openTest(t)
	actor := core.Actor{Kind: "user", ID: "owner"}

	base := t.TempDir()
	baseRepo := makeFakeGitRepo(t, filepath.Join(base, "from-base"), "main", "https://github.com/org/from-base.git")
	oProjRepo := makeFakeGitRepo(t, filepath.Join(t.TempDir(), "from-oproj"), "trunk", "git@github.com:org/from-oproj.git")
	dirRepo := makeFakeGitRepo(t, filepath.Join(t.TempDir(), "from-dir"), "develop", "")

	bs := NewBaseDirStore("")
	if _, err := bs.Add(base, 2, time.Now()); err != nil {
		t.Fatal(err)
	}

	taskWithLegacy := create(t, s, KindTask, map[string]any{
		"title":      "Legacy Task",
		"repo":       "legacy-only",
		"defaultDir": dirRepo,
	})
	_ = create(t, s, KindLane, map[string]any{
		"title": "Lane One",
		"repo":  "lane-repo",
	})

	rep, err := SyncRepos(s, bs, []RepoInput{{Name: "from-oproj", Path: oProjRepo, Source: "o-proj"}}, true, actor)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Created != 5 || len(rep.Repos) != 5 {
		t.Fatalf("sync report = %+v", rep)
	}

	// Re-running sync is idempotent.
	rep2, err := SyncRepos(s, bs, []RepoInput{{Name: "from-oproj", Path: oProjRepo, Source: "o-proj"}}, true, actor)
	if err != nil {
		t.Fatal(err)
	}
	if rep2.Created != 0 || rep2.Updated != 0 || rep2.Skipped != 5 {
		t.Fatalf("second sync report = %+v", rep2)
	}

	// RecordRepos falls back to legacy repo field when no RelRepo exists.
	linked, labels := RecordRepos(s, *taskWithLegacy)
	if len(linked) != 0 || !slices.Equal(labels, []string{"legacy-only"}) {
		t.Fatalf("legacy RecordRepos = %v, %v", linked, labels)
	}

	// Linking RelRepo takes precedence over legacy repo field.
	rRec, err := s.GetByField(KindRepo, "name", "from-base")
	if err != nil {
		t.Fatal(err)
	}
	if rRec.Text("path") != baseRepo || rRec.Text("defaultBranch") != "main" {
		t.Fatalf("base repo = %+v", rRec.Fields)
	}
	if _, _, err := s.Relate(RelateInput{From: taskWithLegacy.Ref(), Kind: RelRepo, To: rRec.Ref()}, actor); err != nil {
		t.Fatal(err)
	}
	linked, labels = RecordRepos(s, *taskWithLegacy)
	if len(linked) != 1 || linked[0].ID != rRec.ID || !slices.Equal(labels, []string{"from-base"}) {
		t.Fatalf("linked RecordRepos = %v, %v", linked, labels)
	}
}

func TestFormSubmissionRelationDiffAndNoListField(t *testing.T) {
	schema, ok := DefaultSchemas().Get(KindTask)
	if !ok {
		t.Fatal("missing KindTask schema")
	}
	r1 := core.Ref{Kind: KindRepo, ID: "r1"}
	r2 := core.Ref{Kind: KindRepo, ID: "r2"}
	r3 := core.Ref{Kind: KindRepo, ID: "r3"}

	visible := []core.ParamSpec{
		{Name: "title", Type: core.ParamString},
		{Name: "repos", Type: core.ParamRef, RefKinds: []string{KindRepo}, Repeatable: true},
	}

	// 1. Create submission: repos excluded from CreateInput.Fields, emitted in RelationDiff.
	createSub := FormSubmission{
		Schema:        schema,
		Submitted:     map[string]any{"title": "Ship repos", "repos": []string{RefText(r1), "repo-two"}},
		VisibleFields: visible,
		ResolveRef: func(kind, raw string) (core.Ref, bool) {
			if kind == KindRepo && raw == "repo-two" {
				return r2, true
			}
			return core.Ref{}, false
		},
	}
	ci := createSub.CreateInput()
	if _, exists := ci.Fields["repos"]; exists {
		t.Fatalf("CreateInput must not store relation field 'repos' in record.Fields: %+v", ci.Fields)
	}
	diffs := createSub.RelationDiff()
	if len(diffs) != 1 || diffs[0].Kind != RelRepo || !slices.Equal(diffs[0].Add, []core.Ref{r1, r2}) || len(diffs[0].Remove) != 0 {
		t.Fatalf("create RelationDiff = %+v", diffs)
	}

	// 2. Edit submission: adding r3 and removing r1 without changing scalar fields sets HasChanges and empty FieldDiff.
	before := &Record{Kind: KindTask, ID: "t1", Fields: map[string]any{"title": "Ship repos"}}
	editSub := FormSubmission{
		Before:          before,
		BeforeRelations: map[string][]core.Ref{RelRepo: {r1, r2}},
		Schema:          schema,
		Submitted:       map[string]any{"title": "Ship repos", "repos": []string{RefText(r2), RefText(r3)}},
		VisibleFields:   visible,
	}
	if !editSub.HasChanges() {
		t.Fatal("expected editSub.HasChanges() == true when only relations changed")
	}
	if fd := editSub.FieldDiff(); len(fd) != 0 {
		t.Fatalf("expected empty FieldDiff, got %+v", fd)
	}
	editDiffs := editSub.RelationDiff()
	if len(editDiffs) != 1 || !slices.Equal(editDiffs[0].Add, []core.Ref{r3}) || !slices.Equal(editDiffs[0].Remove, []core.Ref{r1}) {
		t.Fatalf("edit RelationDiff = %+v", editDiffs)
	}

	// 3. Unchanged relations + unchanged fields => HasChanges == false.
	sameSub := FormSubmission{
		Before:          before,
		BeforeRelations: map[string][]core.Ref{RelRepo: {r1, r2}},
		Schema:          schema,
		Submitted:       map[string]any{"title": "Ship repos", "repos": []string{RefText(r1), RefText(r2)}},
		VisibleFields:   visible,
	}
	if sameSub.HasChanges() || len(sameSub.RelationDiff()) != 0 {
		t.Fatalf("expected no changes, got HasChanges=%v diff=%+v", sameSub.HasChanges(), sameSub.RelationDiff())
	}
}
