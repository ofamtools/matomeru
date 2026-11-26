package app

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/runs"
	"ofam.dev/ochazuke/core"
)

func gitLookup(names ...string) func(string) (string, error) {
	return func(name string) (string, error) {
		if slices.Contains(names, name) {
			return "/tools/" + name, nil
		}
		return "", fmt.Errorf("%s not found", name)
	}
}

func TestGitCommandBuilders(t *testing.T) {
	branch := gitTarget{Dir: "/repo root", Branch: "feature/selected"}
	worktree := gitTarget{Dir: "/linked tree", Worktree: "/linked tree", Branch: branch.Branch}
	for _, tt := range []struct {
		name   string
		action string
		target gitTarget
		path   []string
		bin    string
		args   []string
	}{
		{"extension status", CmdGitStatus, worktree, []string{"o-git", "git"}, "o-git", []string{"status"}},
		{"native status", CmdGitStatus, worktree, []string{"git"}, "git", []string{"status", "--short", "--branch"}},
		{"worktree diff", CmdGitDiff, worktree, []string{"o-git", "git"}, "o-git", []string{"diff"}},
		{"native worktree diff", CmdGitDiff, worktree, []string{"git"}, "git", []string{"diff", "HEAD", "--"}},
		{"branch diff", CmdGitDiff, branch, []string{"o-git", "git"}, "o-git", []string{"diff", "--from", "HEAD", "--to", branch.Branch}},
		{"head log", "git.log", gitTarget{}, []string{"o-git", "git"}, "o-git", []string{"commits"}},
		{"branch log", "git.log", branch, []string{"o-git", "git"}, "git", []string{"log", "--oneline", "-50", branch.Branch, "--"}},
		{"checkout", "git.checkout", branch, []string{"o-git", "git"}, "git", []string{"switch", "--no-guess", "--", branch.Branch}},
		{"merge", "git.merge", branch, []string{"git"}, "git", []string{"merge", "--no-edit", "--", branch.Branch}},
		{"rebase", "git.rebase", branch, []string{"git"}, "git", []string{"rebase", "--", branch.Branch}},
		{"merge-base", "git.merge-base", gitTarget{Branch: branch.Branch, Base: "main"}, []string{"git"}, "git", []string{"merge-base", branch.Branch, "main"}},
		{"sha", "git.sha", branch, []string{"git"}, "git", []string{"rev-parse", "--verify", branch.Branch + "^{commit}"}},
		{"push", "git.push", branch, []string{"git"}, "git", []string{"push", "--", "origin", "refs/heads/" + branch.Branch}},
		{"branches", CmdGitBranches, gitTarget{}, []string{"o-git", "git"}, "o-git", []string{"branches"}},
		{"worktrees", CmdGitWorktrees, worktree, []string{"o-git", "git"}, "o-git", []string{"worktrees"}},
		{"native worktrees", CmdGitWorktrees, worktree, []string{"git"}, "git", []string{"worktree", "list"}},
		{"worktree removal", "git.worktree.remove", worktree, []string{"git"}, "git", []string{"worktree", "remove", "--", "/linked tree"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildGitCommand(tt.action, tt.target, gitLookup(tt.path...))
			if err != nil || got.Executable != "/tools/"+tt.bin || !slices.Equal(got.Args, tt.args) {
				t.Fatalf("buildGitCommand = %+v, %v, want %s %v", got, err, tt.bin, tt.args)
			}
		})
	}

	for _, target := range []gitTarget{{}, {Branch: "--force"}} {
		if _, err := buildGitCommand("git.checkout", target, gitLookup("git")); err == nil {
			t.Errorf("accepted checkout target %+v", target)
		}
	}
	if _, err := buildGitCommand(CmdGitStatus, gitTarget{}, gitLookup()); err == nil || !strings.Contains(err.Error(), "o-git or git") {
		t.Errorf("missing Git dependencies: %v", err)
	}
}

func TestGitHubCommandBuilders(t *testing.T) {
	pr := gitTarget{PR: "https://github.com/other/fork/pull/42/files?tab=checks", Repository: "ofam/base"}
	for _, tt := range []struct {
		name   string
		action string
		target gitTarget
		path   []string
		bin    string
		args   []string
	}{
		{"collection list", CmdGHPRs, gitTarget{}, []string{"o-gh", "gh"}, "o-gh", []string{"pr", "list"}},
		{"checkout list", CmdGHPRs, gitTarget{Dir: "/repo"}, []string{"o-gh", "gh"}, "gh", []string{"pr", "list"}},
		{"project branch list", CmdGHPRs, gitTarget{Repository: "git@github.com:ofam/base.git", Branch: "feature/selected"}, []string{"o-gh", "gh"}, "gh", []string{"pr", "list", "--repo", "ofam/base", "--head", "feature/selected"}},
		{"PR repository list", CmdGHPRs, pr, []string{"gh"}, "gh", []string{"pr", "list", "--repo", "other/fork"}},
		{"extension PR", "gh.pr.view", pr, []string{"o-gh", "gh"}, "o-gh", []string{"pr", "show", "--target", "other/fork#42"}},
		{"native PR", "gh.pr.view", pr, []string{"gh"}, "gh", []string{"pr", "view", "42", "--repo", "other/fork"}},
		{"checks", CmdGHPRChecks, gitTarget{PR: "other/fork#42"}, []string{"o-gh", "gh"}, "gh", []string{"pr", "checks", "42", "--repo", "other/fork"}},
		{"number in checkout", CmdGHPRChecks, gitTarget{PR: "42", Dir: "/repo"}, []string{"gh"}, "gh", []string{"pr", "checks", "42"}},
		{"review", "gh.pr.review", pr, []string{"o-gh", "gh"}, "o-gh", []string{"pr", "open-files", "--target", "other/fork#42"}},
		{"merge", "gh.pr.merge", gitTarget{PR: "42", Repository: "ofam/base", MergeMethod: "squash"}, []string{"gh"}, "gh", []string{"pr", "merge", "42", "--squash", "--repo", "ofam/base"}},
		{"enterprise checks", CmdGHPRChecks, gitTarget{PR: "https://github.example.org/ofam/base/pull/9"}, []string{"gh"}, "gh", []string{"pr", "checks", "9", "--repo", "github.example.org/ofam/base"}},
		{"signals", "gh.signals.poll", pr, []string{"o-gh", "gh"}, "o-gh", []string{"signals", "preview"}},
		{"native signals", "gh.signals.poll", gitTarget{Repository: "ofam/base"}, []string{"gh"}, "gh", []string{"api", "repos/ofam/base/notifications", "--paginate", "--jq", `.[] | select(.subject.type == "PullRequest")`}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildGitHubCommand(tt.action, tt.target, gitLookup(tt.path...))
			if err != nil || got.Executable != "/tools/"+tt.bin || !slices.Equal(got.Args, tt.args) {
				t.Fatalf("buildGitHubCommand = %+v, %v, want %s %v", got, err, tt.bin, tt.args)
			}
		})
	}
	if _, err := buildGitHubCommand("gh.pr.merge", pr, gitLookup("gh")); err == nil || !strings.Contains(err.Error(), "merge method") {
		t.Errorf("merge without an explicit method: %v", err)
	}
	if _, err := buildGitHubCommand(CmdGHPRChecks, gitTarget{}, gitLookup("gh")); err == nil {
		t.Error("checks accepted a missing PR target")
	}
}

func gitTestExecutable(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '%s\\n' \"$PWD\" \"$@\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestGitActionsProjectBindingAndPRMerge(t *testing.T) {
	f := setupNavTreeFixture(t)
	a, d := f.app, f.driver
	gh := gitTestExecutable(t, "gh")

	d.Keys("P", "g", "G")
	pm := a.activePicker(CmdGitHubActions)
	if pm == nil {
		t.Fatalf("g G did not open Git actions; mode=%q focus=%q status=%q", a.Mode(), a.Focus(), a.Status())
	}
	for _, item := range pm.Model().Items {
		if item.ID == CmdGHPRChecks || item.ID == "git.checkout" || item.Subtitle != f.proj.Text("defaultDir") {
			t.Errorf("unexpected project action %+v", item)
		}
	}
	d.Keys("esc")

	pr, _, err := a.Store.Create(data.CreateInput{Kind: data.KindContext, Fields: map[string]any{
		"title": "Pull request 42", "value": "https://github.com/ofam/helios/pull/42",
		"contextKind": "pr", "task": f.subTask.Ref(),
	}}, User)
	if err != nil {
		t.Fatal(err)
	}
	ref := pr.Ref()
	d.Send(core.CommandEvent{Command: core.Command{ID: CmdGitHubActions, Target: &ref, Actor: User}})
	pm = a.activePicker(CmdGitHubActions)
	if pm == nil {
		t.Fatal("PR action picker did not open")
	}
	for i, item := range pm.Model().Items {
		if item.ID == "gh.pr.merge" {
			pm.Model().SelectedIdx = i
			break
		}
	}
	d.Keys("enter")
	merge := a.activePicker("gh.pr.merge")
	if merge == nil {
		t.Fatalf("PR merge did not open the method picker: %s", a.Status())
	}
	merge.Model().SelectedIdx = 1
	d.Keys("enter")
	if a.Space() != SpaceRuns {
		t.Fatalf("selected merge did not open Runs: %q", a.Space())
	}
	result, err := a.Store.Query(data.Query{Kind: data.KindRun})
	if err != nil || len(result.Items) != 1 {
		t.Fatalf("tracked runs = %+v, %v", result, err)
	}
	rec := result.Items[0]
	if task, _ := rec.RefField("task"); task != f.subTask.Ref() || rec.Text("cwd") != f.proj.Text("defaultDir") {
		t.Fatalf("tracked run inherited the wrong context: %+v", rec.Fields)
	}
	value, _ := rec.Value("args")
	if got := fmt.Sprint(value); got != fmt.Sprint([]string{gh, "pr", "merge", "42", "--squash", "--repo", "ofam/helios"}) {
		t.Fatalf("tracked PR merge argv = %s", got)
	}
}

func TestGitStatusStartsTrackedRun(t *testing.T) {
	gitTestExecutable(t, "o-git")
	dir := filepath.Join(t.TempDir(), "checkout with spaces")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	runner := runs.New(store, runs.Options{})
	t.Cleanup(runner.Close)
	a, err := New(store, Options{FS: smokeFS(), Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	d := NewDriver(a, 120, 30)
	d.Send(core.CommandEvent{Command: core.Command{ID: CmdGitStatus, Args: map[string]any{"dir": dir}, Actor: User}})

	result, err := store.Query(data.Query{Kind: data.KindRun})
	if err != nil || len(result.Items) != 1 {
		t.Fatalf("tracked runs = %+v, %v", result, err)
	}
	id := result.Items[0].ID
	select {
	case <-runner.Done(id):
	case <-time.After(3 * time.Second):
		t.Fatalf("Git status run %s did not finish", id)
	}
	rec, err := store.Get(core.Ref{Kind: data.KindRun, ID: id})
	if err != nil {
		t.Fatal(err)
	}
	if output := strings.TrimSpace(rec.Text("output")); rec.Text("runStatus") != runs.StatusDone || output != dir+"\nstatus" {
		t.Fatalf("tracked run state = %q, output = %q", rec.Text("runStatus"), output)
	}
}
