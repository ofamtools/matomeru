package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/widgets/toast"
)

const CmdRepoSync = "repo.sync"

var repoSyncActor = core.Actor{Kind: "system", ID: "mtm.repo", Label: "repo sync"}

func repoSyncSpecs() []core.CommandSpec {
	return []core.CommandSpec{
		spec(CmdRepoSync, "Sync tracked repositories from base directories, o-proj, and existing records", "editing"),
	}
}

type oProjEntry struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Path   string `json:"path"`
	Branch string `json:"branch"`
	Dir    string `json:"dir"`
}

// DiscoverOProjRepos runs `o-proj list` if o-proj is installed on PATH and
// returns the manifest repositories as data.RepoInput entries.
func DiscoverOProjRepos(ctx context.Context) []data.RepoInput {
	bin, err := exec.LookPath("o-proj")
	if err != nil || bin == "" {
		return nil
	}
	runCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(runCtx, bin, "list").Output()
	if err != nil || len(out) == 0 {
		return nil
	}
	var entries []oProjEntry
	if err := json.Unmarshal(out, &entries); err != nil {
		return nil
	}
	repos := make([]data.RepoInput, 0, len(entries))
	for _, e := range entries {
		p := strings.TrimSpace(e.Dir)
		if p == "" {
			p = strings.TrimSpace(e.Path)
		}
		name := strings.TrimSpace(e.Name)
		if name == "" && p == "" {
			continue
		}
		repos = append(repos, data.RepoInput{
			Name:          name,
			Path:          p,
			RemoteURL:     strings.TrimSpace(e.URL),
			DefaultBranch: strings.TrimSpace(e.Branch),
			Source:        "o-proj",
		})
	}
	return repos
}

// SyncRepos runs repository synchronization from the enabled discovery sources
// (configured base directories, o-proj manifest, and existing task/lane records).
func SyncRepos(ctx context.Context, store *data.Store, baseDirs *data.BaseDirStore, useBaseDirs, useOProj, harvest bool, actor core.Actor) (data.RepoSyncReport, error) {
	var bs *data.BaseDirStore
	if useBaseDirs {
		bs = baseDirs
	}
	var oProj []data.RepoInput
	if useOProj {
		oProj = DiscoverOProjRepos(ctx)
	}
	return data.SyncRepos(store, bs, oProj, harvest, actor)
}

func (a *App) repoSyncCmd(s core.State, _ core.Command) core.StepResult {
	var bs *data.BaseDirStore
	if a.files != nil {
		bs = a.files.baseStore
	}
	report, err := SyncRepos(context.Background(), a.Store, bs, true, true, true, User)
	if err != nil {
		a.fail("repo sync", err)
		return core.Next(s)
	}
	msg := fmt.Sprintf("synced repos: %d created, %d updated, %d unchanged (%d total)",
		report.Created, report.Updated, report.Skipped, len(report.Repos))
	a.say("%s", msg)
	a.pushToast(msg, toast.Ok)
	return core.Next(s)
}

// maybeHarvestPaneRepo inspects cwd for a git repository root and registers it
// as a KindRepo record when repo.autoHarvestPanes is enabled. Each directory is
// checked at most once per process.
func (a *App) maybeHarvestPaneRepo(cwd string) {
	if a == nil || a.Store == nil || a.rt == nil || !a.repoAutoHarvestPanesEnabled() {
		return
	}
	dir := strings.TrimSpace(expandHome(cwd))
	if dir == "" {
		return
	}
	a.rt.seenRepoMu.Lock()
	if a.rt.seenRepoDirs == nil {
		a.rt.seenRepoDirs = map[string]bool{}
	}
	if a.rt.seenRepoDirs[dir] {
		a.rt.seenRepoMu.Unlock()
		return
	}
	a.rt.seenRepoDirs[dir] = true
	a.rt.seenRepoMu.Unlock()

	det, ok := data.DetectGitRepoRoot(dir)
	if !ok {
		return
	}
	if det.Path != "" && det.Path != dir {
		a.rt.seenRepoMu.Lock()
		already := a.rt.seenRepoDirs[det.Path]
		a.rt.seenRepoDirs[det.Path] = true
		a.rt.seenRepoMu.Unlock()
		if already {
			return
		}
	}
	det.Source = "pane"
	_, _, _ = data.UpsertRepo(a.Store, det, repoSyncActor)
}
