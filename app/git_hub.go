package app

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/runs"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/widgets/modal"
	"ofam.dev/ochazuke/widgets/picker"
)

const (
	CmdGitHubActions = "git.actions"
	CmdGitStatus     = "git.status"
	CmdGitDiff       = "git.diff"
	CmdGitBranches   = "git.branches"
	CmdGitWorktrees  = "git.worktrees"
	CmdGHPRs         = "gh.prs"
	CmdGHPRChecks    = "gh.pr.checks"
)

type gitTarget struct {
	Dir         string
	Worktree    string
	Branch      string
	PR          string
	Repository  string
	Base        string
	Remote      string
	MergeMethod string
	Task        core.Ref
}

type gitCommand struct {
	Executable string
	Args       []string
}

type gitAction struct {
	id     string
	label  string
	needs  string
	direct bool
}

const (
	gitNeedsBranch   = "branch"
	gitNeedsPR       = "pr"
	gitNeedsWorktree = "worktree"
)

var gitActions = []gitAction{
	{id: CmdGitStatus, label: "Git status", direct: true},
	{id: CmdGitDiff, label: "Git diff", direct: true},
	{id: "git.log", label: "Git log"},
	{id: CmdGitBranches, label: "Git branches", direct: true},
	{id: CmdGitWorktrees, label: "Git worktrees", direct: true},
	{id: "git.checkout", label: "Check out branch", needs: gitNeedsBranch},
	{id: "git.rebase", label: "Rebase current branch onto this branch", needs: gitNeedsBranch},
	{id: "git.merge", label: "Merge this branch into the current branch", needs: gitNeedsBranch},
	{id: "git.merge-base", label: "Find merge base with this branch", needs: gitNeedsBranch},
	{id: "git.sha", label: "Show branch SHA", needs: gitNeedsBranch},
	{id: "git.push", label: "Push branch", needs: gitNeedsBranch},
	{id: "git.show", label: "Show branch tip", needs: gitNeedsBranch},
	{id: "git.worktree.remove", label: "Remove worktree", needs: gitNeedsWorktree},
	{id: CmdGHPRs, label: "List pull requests", direct: true},
	{id: "gh.pr.view", label: "View pull request", needs: gitNeedsPR},
	{id: CmdGHPRChecks, label: "Show pull request checks", needs: gitNeedsPR, direct: true},
	{id: "gh.pr.diff", label: "Show pull request diff", needs: gitNeedsPR},
	{id: "gh.pr.review", label: "Open pull request for review", needs: gitNeedsPR},
	{id: "gh.pr.merge", label: "Merge pull request", needs: gitNeedsPR},
	{id: "gh.pr.open", label: "Open pull request in browser", needs: gitNeedsPR},
	{id: "gh.signals.poll", label: "Poll GitHub pull request signals"},
}

var gitHubMergeMethods = []picker.Item{
	{ID: "merge", Title: "Create a merge commit"},
	{ID: "squash", Title: "Squash and merge"},
	{ID: "rebase", Title: "Rebase and merge"},
}

func selectGitCommand(lookup func(string) (string, error), options ...gitCommand) (gitCommand, error) {
	var names []string
	for _, option := range options {
		names = append(names, option.Executable)
		if path, err := lookup(option.Executable); err == nil && path != "" {
			option.Executable = path
			return option, nil
		}
	}
	return gitCommand{}, fmt.Errorf("%s not found in PATH", strings.Join(names, " or "))
}

func buildGitCommand(action string, target gitTarget, lookup func(string) (string, error)) (gitCommand, error) {
	branch := strings.TrimSpace(target.Branch)
	base := firstNonEmpty(strings.TrimSpace(target.Base), branch)
	remote := firstNonEmpty(strings.TrimSpace(target.Remote), "origin")
	if strings.HasPrefix(branch, "-") || strings.HasPrefix(base, "-") || strings.HasPrefix(remote, "-") {
		return gitCommand{}, fmt.Errorf("Git refs and remotes must not start with '-'")
	}
	ref := firstNonEmpty(branch, "HEAD")
	var extension, native []string
	switch action {
	case CmdGitStatus:
		extension, native = []string{"status"}, []string{"status", "--short", "--branch"}
	case CmdGitDiff:
		extension, native = []string{"diff"}, []string{"diff", "HEAD", "--"}
		if branch != "" && target.Worktree == "" {
			extension = []string{"diff", "--from", "HEAD", "--to", branch}
			native = []string{"diff", "HEAD", branch, "--"}
		}
	case "git.log":
		native = []string{"log", "--oneline", "-50", ref, "--"}
		// The extension lists HEAD and comparisons. A selected ref needs its own history.
		if branch == "" {
			extension = []string{"commits"}
		}
	case CmdGitBranches:
		extension, native = []string{"branches"}, []string{"branch", "--list", "--no-color"}
	case CmdGitWorktrees:
		extension, native = []string{"worktrees"}, []string{"worktree", "list"}
	case "git.checkout":
		if branch == "" {
			return gitCommand{}, fmt.Errorf("checkout requires a branch")
		}
		// o-git accepts fuzzy branch queries. A pointer targets an exact ref.
		native = []string{"switch", "--no-guess", "--", branch}
	case "git.rebase":
		if base == "" {
			return gitCommand{}, fmt.Errorf("rebase requires a target ref")
		}
		native = []string{"rebase", "--", base}
	case "git.merge":
		if base == "" {
			return gitCommand{}, fmt.Errorf("merge requires a target ref")
		}
		native = []string{"merge", "--no-edit", "--", base}
	case "git.merge-base":
		if base == "" {
			return gitCommand{}, fmt.Errorf("merge-base requires a target ref")
		}
		left := "HEAD"
		if target.Base != "" {
			left = ref
		}
		native = []string{"merge-base", left, base}
	case "git.sha":
		native = []string{"rev-parse", "--verify", ref + "^{commit}"}
	case "git.push":
		if branch == "" {
			return gitCommand{}, fmt.Errorf("push requires a branch")
		}
		native = []string{"push", "--", remote, "refs/heads/" + branch}
	case "git.show":
		native = []string{"show", ref, "--"}
	case "git.worktree.remove":
		path := firstNonEmpty(target.Worktree, target.Dir)
		if path == "" {
			return gitCommand{}, fmt.Errorf("worktree removal requires a directory")
		}
		native = []string{"worktree", "remove", "--", path}
	default:
		return gitCommand{}, fmt.Errorf("unknown Git action %q", action)
	}
	options := []gitCommand{{Executable: "git", Args: native}}
	if len(extension) > 0 {
		options = append([]gitCommand{{Executable: "o-git", Args: extension}}, options...)
	}
	return selectGitCommand(lookup, options...)
}

var (
	gitHubRepositoryPart = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)
	gitHubPRNumber       = regexp.MustCompile(`^[1-9][0-9]*$`)
)

func gitHubRepository(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "/") || strings.HasPrefix(value, "~/") {
		return ""
	}
	if _, address, ok := strings.Cut(value, "@"); ok && !strings.Contains(value, "://") {
		if host, path, ok := strings.Cut(address, ":"); ok {
			value = host + "/" + path
		}
	}
	if u, err := url.Parse(value); err == nil && u.Host != "" {
		value = u.Host + u.Path
	}
	parts := strings.Split(strings.Trim(value, "/"), "/")
	if len(parts) == 3 && strings.EqualFold(parts[0], "github.com") {
		parts = parts[1:]
	}
	if len(parts) < 2 || len(parts) > 3 {
		return ""
	}
	if len(parts) == 3 && !strings.Contains(parts[0], ".") {
		return ""
	}
	parts[len(parts)-1] = strings.TrimSuffix(parts[len(parts)-1], ".git")
	for _, part := range parts {
		if !gitHubRepositoryPart.MatchString(part) {
			return ""
		}
	}
	return strings.Join(parts, "/")
}

// gitHubPRArgs resolves an explicit pull request independently of the current
// repository. Bare numbers use the selected repository or the command's cwd.
func gitHubPRArgs(value, repo string) (number, repository, extensionTarget string) {
	value = strings.TrimSpace(value)
	repository = gitHubRepository(repo)
	switch {
	case gitHubPRNumber.MatchString(value):
		number = value
	case strings.Contains(value, "#") && !strings.Contains(value, "://"):
		left, right, _ := strings.Cut(value, "#")
		repository, number = gitHubRepository(strings.TrimPrefix(left, "pr/")), right
		if repository == "" {
			return "", "", ""
		}
	default:
		u, err := url.Parse(value)
		if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
			return "", "", ""
		}
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) < 4 || parts[2] != "pull" {
			return "", "", ""
		}
		number, repository = parts[3], gitHubRepository(u.Host+"/"+parts[0]+"/"+parts[1])
		if repository == "" {
			return "", "", ""
		}
	}
	if !gitHubPRNumber.MatchString(number) {
		return "", "", ""
	}
	if strings.Count(repository, "/") == 1 {
		extensionTarget = repository + "#" + number
	}
	return number, repository, extensionTarget
}

func buildGitHubCommand(action string, target gitTarget, lookup func(string) (string, error)) (gitCommand, error) {
	repo := gitHubRepository(target.Repository)
	var extension, native []string
	switch action {
	case CmdGHPRs:
		if _, prRepo, _ := gitHubPRArgs(target.PR, target.Repository); prRepo != "" {
			repo = prRepo
		}
		native = []string{"pr", "list"}
		if repo != "" {
			native = append(native, "--repo", repo)
		}
		if target.Branch != "" {
			native = append(native, "--head", target.Branch)
		}
		// o-gh lists account collections. gh can list a specific checkout,
		// repository or head branch without changing the user's collections.
		if target.Dir == "" && repo == "" && target.Branch == "" {
			extension = []string{"pr", "list"}
		}
	case "gh.signals.poll":
		extension = []string{"signals", "preview"}
		endpoint := "notifications"
		host := ""
		if strings.Count(repo, "/") == 2 {
			host, repo, _ = strings.Cut(repo, "/")
		}
		if repo != "" {
			endpoint = "repos/" + repo + "/notifications"
		}
		native = []string{"api", endpoint, "--paginate", "--jq", `.[] | select(.subject.type == "PullRequest")`}
		if host != "" {
			native = append(native, "--hostname", host)
		}
	default:
		number, repo, extensionTarget := gitHubPRArgs(target.PR, target.Repository)
		if number == "" {
			return gitCommand{}, fmt.Errorf("this action requires a GitHub pull request URL, owner/repo#number, or number")
		}
		switch action {
		case "gh.pr.view":
			extension, native = []string{"pr", "show"}, []string{"pr", "view", number}
		case CmdGHPRChecks:
			native = []string{"pr", "checks", number}
		case "gh.pr.diff":
			native = []string{"pr", "diff", number}
		case "gh.pr.review":
			extension, native = []string{"pr", "open-files"}, []string{"pr", "view", number, "--web"}
		case "gh.pr.merge":
			switch target.MergeMethod {
			case "merge", "squash", "rebase":
				native = []string{"pr", "merge", number, "--" + target.MergeMethod}
			default:
				return gitCommand{}, fmt.Errorf("select a merge method: merge, squash, or rebase")
			}
		case "gh.pr.open":
			extension, native = []string{"pr", "open"}, []string{"pr", "view", number, "--web"}
		default:
			return gitCommand{}, fmt.Errorf("unknown GitHub action %q", action)
		}
		if len(extension) > 0 && extensionTarget != "" {
			extension = append(extension, "--target", extensionTarget)
		} else {
			extension = nil
		}
		if repo != "" {
			native = append(native, "--repo", repo)
		}
	}
	options := []gitCommand{{Executable: "gh", Args: native}}
	if len(extension) > 0 {
		options = append([]gitCommand{{Executable: "o-gh", Args: extension}}, options...)
	}
	return selectGitCommand(lookup, options...)
}

func gitHubSpecs() []core.CommandSpec {
	specs := []core.CommandSpec{spec(CmdGitHubActions, "Pick Git and GitHub actions for the focused target")}
	for _, action := range gitActions {
		if action.direct {
			specs = append(specs, spec(action.id, action.label))
		}
	}
	return specs
}

func gitHubBindings() []core.Binding {
	return []core.Binding{bind(ModeNormal, "", "g G", CmdGitHubActions, "Git and GitHub actions")}
}

func (a *App) registerGitHubHandlers() {
	a.handle(CmdGitHubActions, a.gitHubActions)
	for _, action := range gitActions {
		if action.direct {
			a.handle(action.id, func(s core.State, c core.Command) core.StepResult {
				target := a.gitActionTarget(s, c)
				return core.Next(a.runGitHubAction(s, action.id, target))
			})
		}
	}
}

func (a *App) gitActionTarget(s core.State, c core.Command) gitTarget {
	var target gitTarget
	ref, ok := a.subject(s, c)
	if ok {
		target = a.gitTargetForRef(ref)
	}
	if c.Target == nil {
		if projKey := a.activeProjectWorkspaceKey(s); projKey != "" {
			pc := a.projectCtrl()
			nav, found := pc.navTarget(projKey, s.ActiveFrame().FocusID)
			if !found {
				if active := pc.activeNavItem(projKey); active != nil {
					nav = *active
				}
			}
			if nav.Worktree != nil {
				target.Dir = nav.Worktree.Path
				target.Worktree = nav.Worktree.Path
				target.Branch = nav.Worktree.Branch
				target.Repository = firstNonEmpty(nav.Worktree.Repo, target.Repository)
			}
			if nav.ContextItem != nil && data.ClassifyContextKind(nav.ContextItem.Value, nav.ContextItem.Kind) == "pr" {
				target.PR = nav.ContextItem.Value
			}
			if target.Task.ID == "" {
				projectID := a.activeProjectOutlineID(s)
				target.Task = core.Ref{Kind: data.KindTask, ID: projectID}
			}
		}
		if chip, found := pointerChipAt(a.View(s), s.ActiveFrame().FocusID); found {
			target = gitTargetWithChip(target, chip)
		}
	}
	if target.Task.ID != "" {
		cwd, projectID := a.resolveTaskDirAndProject(target.Task.ID)
		target.Dir = firstNonEmpty(target.Dir, cwd)
		for _, taskID := range []string{target.Task.ID, projectID} {
			if target.Repository != "" || taskID == "" {
				break
			}
			if rec, err := a.Store.Get(core.Ref{Kind: data.KindTask, ID: taskID}); err == nil {
				target.Repository = rec.Text("repo")
			}
		}
	} else if target.Dir == "" && a.opts.FS == nil {
		target.Dir, _ = os.Getwd()
	}
	target.Dir = expandHome(firstNonEmpty(c.Arg("dir"), target.Dir))
	target.Worktree = expandHome(target.Worktree)
	target.Branch = firstNonEmpty(c.Arg("branch"), target.Branch)
	target.PR = firstNonEmpty(c.Arg("pr"), target.PR)
	target.Repository = firstNonEmpty(c.Arg("repo"), target.Repository)
	target.Base = firstNonEmpty(c.Arg("base"), target.Base)
	target.Remote = firstNonEmpty(c.Arg("remote"), target.Remote)
	return target
}

func (a *App) gitTargetForRef(ref core.Ref) gitTarget {
	var target gitTarget
	rec, err := a.Store.Get(ref)
	if err != nil || rec == nil {
		if cached, found := a.cachedExternal(ref); found {
			rec = &cached
		}
	}
	if rec == nil {
		switch ref.Kind {
		case "branch", "git.branch", "git.ref":
			target.Branch = ref.ID
		case "pr", "context.pr", "gh.pr":
			target.PR = ref.ID
		case "directory", "dir":
			target.Dir = ref.ID
		case "worktree", "git.worktree":
			target.Dir, target.Worktree = ref.ID, ref.ID
		}
		return target
	}
	target.Task, _ = rec.RefField("task")
	if rec.Kind == data.KindTask {
		target.Task = ref
	}
	target.Dir = firstNonEmpty(rec.Text("worktree"), rec.Text("cwd"), rec.Text("defaultDir"))
	target.Worktree = rec.Text("worktree")
	target.Branch = rec.Text("branch")
	target.Repository = firstNonEmpty(rec.Text("repo"), rec.Text("repository"))
	switch rec.Kind {
	case "directory", "dir":
		target.Dir = firstNonEmpty(target.Dir, rec.Text("path"))
	case "worktree", "git.worktree":
		target.Dir = firstNonEmpty(target.Dir, rec.Text("path"))
		target.Worktree = target.Dir
	case "branch", "git.branch", "git.ref":
		if repo := rec.Text("repo"); strings.HasPrefix(repo, "/") || strings.HasPrefix(repo, "~/") {
			target.Dir = firstNonEmpty(target.Dir, repo)
		}
		target.Branch = firstNonEmpty(target.Branch, rec.Text("name"), ref.ID)
	case "pr", "context.pr", "gh.pr":
		target.PR = firstNonEmpty(rec.Text("url"), rec.Text("key"), rec.Text("number"), rec.Text("value"), ref.ID)
	case data.KindContext:
		value := rec.Text("value")
		if data.ClassifyContextKind(value, rec.Text("contextKind")) == "pr" {
			target.PR = value
		}
	}
	return target
}

func (a *App) gitHubActions(s core.State, c core.Command) core.StepResult {
	target := a.gitActionTarget(s, c)
	items := make([]picker.Item, 0, len(gitActions))
	for _, action := range gitActions {
		subtitle := target.Dir
		switch action.needs {
		case gitNeedsBranch:
			if target.Branch == "" {
				continue
			}
			subtitle = target.Branch
		case gitNeedsPR:
			if number, _, _ := gitHubPRArgs(target.PR, target.Repository); number == "" {
				continue
			}
			subtitle = target.PR
		case gitNeedsWorktree:
			if target.Worktree == "" {
				continue
			}
			subtitle = target.Worktree
		}
		items = append(items, picker.Item{ID: action.id, Title: action.label, Subtitle: subtitle})
	}
	pm := picker.New("Git and GitHub actions", items)
	m := a.newPickerModal(pm, modal.PickerOptions{
		ID:       CmdGitHubActions,
		PageSize: 12,
		OnConfirm: func(picked picker.Item) modal.Result {
			a.afterModal(func(st core.State) core.State {
				return a.runGitHubAction(st, picked.ID, target)
			})
			return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
		},
	})
	return core.Next(a.pushModal(s, m))
}

func (a *App) runGitHubAction(s core.State, actionID string, target gitTarget) core.State {
	target.Dir = expandHome(target.Dir)
	target.Worktree = expandHome(target.Worktree)
	if actionID == "gh.pr.merge" && target.MergeMethod == "" {
		if number, _, _ := gitHubPRArgs(target.PR, target.Repository); number != "" {
			return a.pickGitHubMergeMethod(s, target)
		}
	}
	var command gitCommand
	var err error
	if strings.HasPrefix(actionID, "gh.") {
		command, err = buildGitHubCommand(actionID, target, exec.LookPath)
	} else {
		command, err = buildGitCommand(actionID, target, exec.LookPath)
	}
	if err != nil {
		a.fail(actionID, err)
		return s
	}
	title := actionID
	for _, action := range gitActions {
		if action.id == actionID {
			title = action.label
			break
		}
	}
	return a.startTrackedCommand(s, title, target.Dir, command.Executable, command.Args, target.Task)
}

func (a *App) pickGitHubMergeMethod(s core.State, target gitTarget) core.State {
	pm := picker.New("Merge pull request · "+target.PR, gitHubMergeMethods)
	return a.pushModal(s, a.newPickerModal(pm, modal.PickerOptions{
		ID: "gh.pr.merge",
		OnConfirm: func(picked picker.Item) modal.Result {
			a.afterModal(func(st core.State) core.State {
				target.MergeMethod = picked.ID
				return a.runGitHubAction(st, "gh.pr.merge", target)
			})
			return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
		},
	}))
}

// startTrackedCommand uses argv so paths, refs and other words retain their
// meaning when a run is started or rerun.
func (a *App) startTrackedCommand(s core.State, title, dir, executable string, args []string, task core.Ref) core.State {
	argv := append([]string{executable}, args...)
	dir = expandHome(dir)
	var rec *data.Record
	var err error
	if a.rt.Runner != nil {
		rec, err = a.rt.Runner.Start(context.Background(), runs.StartSpec{
			Title: title, Args: argv, Dir: dir, TaskRef: task, Actor: runActor,
		})
		if rec != nil {
			s = a.openSpace(s, SpaceRuns)
		}
		return a.started(s, "ran "+title, rec, err)
	}
	fields := map[string]any{"title": title, "command": strings.Join(argv, " "), "args": argv, "runStatus": "queued"}
	if dir != "" {
		fields["cwd"] = dir
	}
	if task.ID != "" {
		fields["task"] = task
	}
	rec, cs, err := a.Store.Create(data.CreateInput{Kind: data.KindRun, Fields: fields}, User)
	a.wrote("queued "+title, cs, err)
	if err == nil && rec != nil {
		s = a.openSpace(s, SpaceRuns)
		a.viewMemo.gen++
		selectRow(a.records(runsTable, data.KindRun), data.RefText(rec.Ref()))
	}
	return s
}
